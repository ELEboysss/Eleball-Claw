package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/llm"
)

// Agent Team P2：组共享记忆相关常量
const (
	// TeamMemoryInjectMaxChars 注入 system prompt 的「组共享记忆」区块字符预算（≈2000 tokens）
	TeamMemoryInjectMaxChars = 4000
	// teamMemoryCandidateLimit 检索评分的候选集大小（组内最近 N 条）
	teamMemoryCandidateLimit = 200
	// teamMemoryMaxContentRunes 手动新增/提取单条记忆的内容上限（字符）
	teamMemoryMaxContentRunes = 500
	// teamMemoryExtractItemRunes 提取单条记忆的内容上限（字符）
	teamMemoryExtractItemRunes = 200
	// teamMemoryDedupSnippetRunes LIKE 粗去重的内容片段长度（前后各取）
	teamMemoryDedupSnippetRunes = 10
	// AR-09 记忆升级常量
	// teamMemoryEmbeddingMinSimilarity embedding 检索的最低余弦相似度阈值（低于则不命中）
	teamMemoryEmbeddingMinSimilarity = 0.30
	// teamMemoryConsolidateThreshold 组内 active 记忆数超此值触发合并/遗忘
	teamMemoryConsolidateThreshold = 25
	// teamMemoryConsolidateBatch 单次合并 prompt 处理的记忆条数上限
	teamMemoryConsolidateBatch = 60
	// teamMemoryForgetTTLDays 未命中归档天数
	teamMemoryForgetTTLDays = 90
)

// MemoryStore 记忆存储抽象（AR-09）：隔离检索/合并/遗忘的实现，便于 embedding 与 LIKE 策略切换。
// TeamMemoryService 实现该接口；检索优先 embedding 余弦相似度，embedder 缺失时降级 LIKE。
type MemoryStore interface {
	// Search 检索用于 system prompt 注入的记忆（topN 条）。
	Search(ctx context.Context, userID, teamID, query string, topN int) []model.TeamMemory
	// Consolidate 合并/去重/去矛盾的 active 记忆，被取代的条目标记 superseded。
	// client/modelName 由调用方传入（复用本次执行的用户客户端，计费归该用户）。
	Consolidate(ctx context.Context, client AgentLLMClient, modelName, teamID string) error
	// Forget 归档长期（默认 90 天）未命中的 active 记忆。
	Forget(ctx context.Context, teamID string) error
}

// TeamMemoryService 组共享记忆业务逻辑（Agent Team P2 + AR-09 记忆升级）。
// 检索实现集中在 SearchForInjection，与调用方隔离：
//   - embedder 可用时：embedding 余弦相似度 + 时间衰减，LIKE 作降级；
//   - embedder 为 nil（claw 本地无 embedding 服务）：退回 LIKE 命中加权 + 时间衰减。
type TeamMemoryService struct {
	repo *repository.TeamMemoryRepo
	team *TeamService
	// embedder AR-09：向量嵌入客户端（可空）。nil 或 embeddingModel 为空时检索降级 LIKE。
	embedder       llm.EmbeddingClient
	embeddingModel string
}

// NewTeamMemoryService 创建服务（embedder 默认 nil，检索降级 LIKE；通过 SetEmbedder 启用向量检索）
func NewTeamMemoryService(repo *repository.TeamMemoryRepo, team *TeamService) *TeamMemoryService {
	return &TeamMemoryService{repo: repo, team: team}
}

// SetEmbedder 装配 embedding 客户端与模型名（AR-09）。model 为空时即便 client 非 nil 也降级 LIKE。
func (s *TeamMemoryService) SetEmbedder(client llm.EmbeddingClient, model string) {
	s.embedder = client
	s.embeddingModel = model
}

// SearchForInjection 组内检索用于 system prompt 注入的记忆（实现 MemoryStore.Search）。
//
// 检索策略（AR-09）：
//   - embedder 可用且 query 非空：embedding 余弦相似度（≥阈值）× 时间衰减，取 topN；
//   - 否则或 embedding 无命中：降级为关键词 LIKE 命中加权（Content +2 / Tags +3）× 时间衰减；
//   - query 为空：按时间倒序取 topN。
//
// 命中条目回写 LastHitAt（供 Forget 判定未命中）。组归属校验失败或检索出错返回空切片
// （注入是增强项，不阻断主流程）。
func (s *TeamMemoryService) SearchForInjection(ctx context.Context, userID, teamID, query string, topN int) []model.TeamMemory {
	if topN <= 0 {
		topN = 8
	}
	if err := s.team.CheckOwned(userID, teamID); err != nil {
		return []model.TeamMemory{}
	}
	candidates, err := s.repo.ListActiveByTeam(teamID, teamMemoryCandidateLimit)
	if err != nil || len(candidates) == 0 {
		return []model.TeamMemory{}
	}

	// 优先 embedding 检索；失败或无命中则降级 LIKE
	if s.embedder != nil && s.embeddingModel != "" && strings.TrimSpace(query) != "" {
		if hits := s.searchByEmbedding(ctx, query, candidates, topN); len(hits) > 0 {
			s.touchLastHit(hits)
			return hits
		}
	}

	result := s.searchByKeyword(candidates, query, topN)
	s.touchLastHit(result)
	return result
}

// searchByEmbedding 用 embedding 余弦相似度 + 时间衰减检索（AR-09）。
// 候选中无向量的旧条目跳过；相似度低于阈值的不命中。返回 nil 表示无可用命中（调用方降级 LIKE）。
func (s *TeamMemoryService) searchByEmbedding(ctx context.Context, query string, candidates []model.TeamMemory, topN int) []model.TeamMemory {
	vecs, err := s.embedder.Embed(ctx, s.embeddingModel, []string{query})
	if err != nil || len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil
	}
	qv := vecs[0]
	now := time.Now().Unix()
	type scored struct {
		mem   model.TeamMemory
		score float64
	}
	hits := make([]scored, 0, len(candidates))
	for _, m := range candidates {
		v := llm.DecodeFloat32Vector(m.Embedding)
		if len(v) == 0 {
			continue // 无向量的旧条目不参与向量检索
		}
		sim := llm.CosineSimilarity(qv, v)
		if sim < teamMemoryEmbeddingMinSimilarity {
			continue
		}
		ageDays := float64(now-m.CreatedAt) / 86400
		if ageDays < 0 {
			ageDays = 0
		}
		decay := 1.0 / (1.0 + ageDays/30.0)
		hits = append(hits, scored{mem: m, score: sim * decay})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].mem.CreatedAt > hits[j].mem.CreatedAt
	})
	if len(hits) > topN {
		hits = hits[:topN]
	}
	result := make([]model.TeamMemory, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.mem)
	}
	return result
}

// searchByKeyword 关键词 LIKE 命中加权 + 时间衰减检索（原 P2 方案，AR-09 作为 embedding 降级路径）。
// query 为空时按时间倒序取 topN（candidates 已倒序）。
func (s *TeamMemoryService) searchByKeyword(candidates []model.TeamMemory, query string, topN int) []model.TeamMemory {
	tokens := tokenizeMemoryQuery(query)
	// 无有效关键词：直接按时间倒序取 topN
	if len(tokens) == 0 {
		if len(candidates) > topN {
			candidates = candidates[:topN]
		}
		return candidates
	}
	now := time.Now().Unix()
	type scored struct {
		mem   model.TeamMemory
		score float64
	}
	hits := make([]scored, 0, len(candidates))
	for _, m := range candidates {
		content := strings.ToLower(m.Content)
		tags := strings.ToLower(m.Tags)
		weight := 0.0
		for _, tok := range tokens {
			if strings.Contains(content, tok) {
				weight += 2
			}
			if tags != "" && strings.Contains(tags, tok) {
				weight += 3
			}
		}
		if weight <= 0 {
			continue // 未命中关键词的条目不参与注入
		}
		// 时间衰减：30 天半衰，越新的记忆权重越高
		ageDays := float64(now-m.CreatedAt) / 86400
		if ageDays < 0 {
			ageDays = 0
		}
		decay := 1.0 / (1.0 + ageDays/30.0)
		hits = append(hits, scored{mem: m, score: weight * decay})
	}
	// 相关度降序；同分按时间倒序（新在前）
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].mem.CreatedAt > hits[j].mem.CreatedAt
	})
	if len(hits) > topN {
		hits = hits[:topN]
	}
	result := make([]model.TeamMemory, 0, len(hits))
	for _, h := range hits {
		result = append(result, h.mem)
	}
	return result
}

// touchLastHit 回写命中条目的最近命中时间（AR-09，失败不阻断检索主流程）
func (s *TeamMemoryService) touchLastHit(hits []model.TeamMemory) {
	if len(hits) == 0 {
		return
	}
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	_ = s.repo.TouchLastHit(ids, time.Now().Unix())
}

// FormatInjectionBlock 将检索命中的记忆格式化为「组共享记忆」区块（拼入 system prompt，不新增消息）。
// maxChars 为字符预算（默认 4000，≈2000 tokens），超出按相关度顺序截断丢弃；
// memories 为空或预算不足时返回空字符串。
func (s *TeamMemoryService) FormatInjectionBlock(memories []model.TeamMemory, maxChars int) string {
	if len(memories) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = TeamMemoryInjectMaxChars
	}
	header := "组共享记忆（同组其他对话沉淀的事实，供参考，可能过期）："
	var sb strings.Builder
	sb.WriteString(header)
	for _, m := range memories {
		line := "\n- " + m.Content
		if m.Tags != "" {
			line += " [" + m.Tags + "]"
		}
		// 按字符预算截断：当前行放不下则停止（memories 已按相关度排序）
		if len([]rune(sb.String()))+len([]rune(line)) > maxChars {
			break
		}
		sb.WriteString(line)
	}
	// 一条都放不下时视为无注入
	if sb.String() == header {
		return ""
	}
	return sb.String()
}

// AddMemory 手动新增组记忆（校验组归属；content 必填且不超过 500 字）
func (s *TeamMemoryService) AddMemory(userID, teamID, content, tags, sourceConversationID string) (*model.TeamMemory, error) {
	if _, err := s.team.getOwned(userID, teamID); err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, errors.New("记忆内容不能为空")
	}
	if len([]rune(content)) > teamMemoryMaxContentRunes {
		return nil, fmt.Errorf("记忆内容不能超过 %d 字", teamMemoryMaxContentRunes)
	}
	now := time.Now().Unix()
	m := &model.TeamMemory{
		ID:                   generateID("tm"),
		TeamID:               teamID,
		UserID:               userID,
		Content:              content,
		Tags:                 strings.TrimSpace(tags),
		SourceConversationID: sourceConversationID,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.repo.Create(m); err != nil {
		return nil, err
	}
	return m, nil
}

// ListMemories 分页查询组记忆（校验组归属，按 created_at 倒序）
func (s *TeamMemoryService) ListMemories(userID, teamID string, page, pageSize int) ([]model.TeamMemory, int64, error) {
	if _, err := s.team.getOwned(userID, teamID); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	items, total, err := s.repo.ListByTeam(teamID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []model.TeamMemory{}
	}
	return items, total, nil
}

// DeleteMemory 删除组记忆条目（校验组归属，且条目属于该组）
func (s *TeamMemoryService) DeleteMemory(userID, teamID, memoryID string) error {
	if _, err := s.team.getOwned(userID, teamID); err != nil {
		return err
	}
	m, err := s.repo.GetByID(memoryID)
	if err != nil {
		return errors.New("记忆不存在")
	}
	if m.TeamID != teamID {
		return errors.New("记忆不属于该组")
	}
	return s.repo.Delete(memoryID)
}

// ExtractAndStore 单 pass ADD-only 记忆提取（Agent Team P2，execute 成功后异步调用）：
// 用一次 Chat 调用让模型从「用户消息 + 最终回答」提取值得沉淀的事实（偏好/结论/实体信息），
// 解析行、LIKE 粗去重后写入。失败只返回 error，由调用方记日志，不影响主流程。
func (s *TeamMemoryService) ExtractAndStore(ctx context.Context, client AgentLLMClient, modelName, teamID, userID, conversationID, userMessage, finalAnswer string) error {
	if client == nil || teamID == "" || strings.TrimSpace(finalAnswer) == "" {
		return nil
	}
	prompt := "你是记忆提取器。从下面的「用户消息」和「助手回答」中提取值得长期沉淀的事实（用户偏好、明确结论、关键实体信息）。\n" +
		"要求：\n" +
		"- 每行一条，每条不超过 200 字；\n" +
		"- 只输出事实条目本身，不要寒暄、不要解释、不要序号以外的多余格式；\n" +
		"- 不要提取临时性、一次性的内容；\n" +
		"- 如果没有值得记忆的内容，只回复「无」。\n\n" +
		"用户消息：\n" + userMessage + "\n\n" +
		"助手回答：\n" + finalAnswer
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Model: modelName,
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("记忆提取模型无响应")
	}

	var newItems []*model.TeamMemory
	for _, line := range strings.Split(resp.Delta, "\n") {
		item := normalizeExtractedMemoryLine(line)
		if item == "" {
			continue
		}
		// LIKE 粗去重：同组内已有 Content 包含该条目前/后 10 字符片段的记忆则跳过
		dup, err := s.isDuplicateMemory(teamID, item)
		if err != nil {
			return err
		}
		if dup {
			continue
		}
		now := time.Now().Unix()
		m := &model.TeamMemory{
			ID:                   generateID("tm"),
			TeamID:               teamID,
			UserID:               userID,
			Content:              item,
			SourceConversationID: conversationID,
			Status:               "active",
			LastHitAt:            now,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		if err := s.repo.Create(m); err != nil {
			return err
		}
		newItems = append(newItems, m)
	}

	// AR-09：批量回填 embedding（embedder 可用时）。失败不阻断--记忆已写入，仅缺向量，后续 LIKE 仍可检索。
	if s.embedder != nil && s.embeddingModel != "" && len(newItems) > 0 {
		texts := make([]string, len(newItems))
		for i, m := range newItems {
			texts[i] = m.Content
		}
		if vecs, e := s.embedder.Embed(ctx, s.embeddingModel, texts); e == nil {
			for i, v := range vecs {
				if i < len(newItems) && len(v) > 0 {
					_ = s.repo.UpdateEmbedding(newItems[i].ID, llm.EncodeFloat32Vector(v))
				}
			}
		}
	}

	// AR-09：惰性触发合并/遗忘（组内 active 记忆数超阈值时，复用本次客户端计费归该用户）
	if count, err := s.repo.CountActiveByTeam(teamID); err == nil && count >= int64(teamMemoryConsolidateThreshold) {
		_ = s.Consolidate(ctx, client, modelName, teamID)
		_ = s.Forget(ctx, teamID)
	}
	return nil
}

// normalizeExtractedMemoryLine 清洗提取结果的一行：去空白/项目符号/序号，「无」短路，超长截断
func normalizeExtractedMemoryLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	// 去掉行首的项目符号与序号（如 "- "、"• "、"1. "、"1、"）
	line = strings.TrimLeft(line, "-•*· \t")
	if idx := strings.IndexAny(line, ".、）)"); idx > 0 && idx <= 3 {
		prefix := strings.TrimSpace(line[:idx])
		if prefix != "" && strings.Trim(prefix, "0123456789") == "" {
			line = strings.TrimSpace(line[idx+1:])
		}
	}
	if line == "" || line == "无" {
		return ""
	}
	runes := []rune(line)
	if len(runes) > teamMemoryExtractItemRunes {
		line = string(runes[:teamMemoryExtractItemRunes])
	}
	return line
}

// isDuplicateMemory LIKE 粗去重：同组内已存在 Content/Tags 包含该条目
// 前 10 或后 10 字符片段的记忆则视为重复（Content 不足 10 字符时用整条）
func (s *TeamMemoryService) isDuplicateMemory(teamID, content string) (bool, error) {
	runes := []rune(content)
	snippets := []string{content}
	if len(runes) > teamMemoryDedupSnippetRunes {
		snippets = []string{
			string(runes[:teamMemoryDedupSnippetRunes]),
			string(runes[len(runes)-teamMemoryDedupSnippetRunes:]),
		}
	}
	for _, snip := range snippets {
		items, err := s.repo.SearchByKeyword(teamID, snip, 1)
		if err != nil {
			return false, err
		}
		for _, m := range items {
			if strings.Contains(m.Content, snip) {
				return true, nil
			}
		}
	}
	return false, nil
}

// tokenizeMemoryQuery 查询分词（从简）：按空白切分 + CJK 连续段 bigram；全部转小写
func tokenizeMemoryQuery(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	seen := make(map[string]struct{})
	tokens := make([]string, 0, 8)
	add := func(tok string) {
		if tok == "" {
			return
		}
		if _, ok := seen[tok]; ok {
			return
		}
		seen[tok] = struct{}{}
		tokens = append(tokens, tok)
	}
	for _, field := range strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) {
		add(field)
		// CJK 连续段追加 bigram，提升中文短词命中率
		runes := []rune(field)
		start := -1
		flush := func(end int) {
			if start < 0 || end-start < 2 {
				return
			}
			for i := start; i+1 < end; i++ {
				add(string(runes[i : i+2]))
			}
		}
		for i, r := range runes {
			if isCJKRune(r) {
				if start < 0 {
					start = i
				}
			} else {
				flush(i)
				start = -1
			}
		}
		flush(len(runes))
	}
	return tokens
}

// isCJKRune 判断 rune 是否为 CJK 表意文字
func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

// Consolidate 合并/去重/去矛盾的 active 记忆（实现 MemoryStore.Consolidate，AR-09）。
// 取组内最近一批 active 记忆，让 LLM 找出语义重复/矛盾条目：
//   - DROP <编号>：纯冗余条目，标记 superseded；
//   - MERGE <编号列表> | <合并内容>：创建合并后的新记忆（含 embedding），原条目标记 superseded。
//
// 解析容错；LLM 调用失败或无指令则不改动。client/modelName 复用本次执行的用户客户端。
func (s *TeamMemoryService) Consolidate(ctx context.Context, client AgentLLMClient, modelName, teamID string) error {
	if client == nil || teamID == "" {
		return nil
	}
	memories, err := s.repo.ListActiveByTeam(teamID, teamMemoryConsolidateBatch)
	if err != nil {
		return err
	}
	if len(memories) < teamMemoryConsolidateThreshold {
		return nil
	}
	var sb strings.Builder
	for i, m := range memories {
		fmt.Fprintf(&sb, "%d. %s\n", i+1, m.Content)
	}
	prompt := "你是记忆合并器。下面是同一组的记忆条目（编号. 内容）。请找出语义重复或相互矛盾的条目并合并：\n" +
		"- 对纯重复/冗余的条目，输出 `DROP <编号>`（保留其他更完整的同义条目）；\n" +
		"- 对可合并提炼的多条，输出 `MERGE <编号1,编号2,...> | <合并后的一句话内容>（≤200 字）`，原条目将被取代；\n" +
		"- 一次输出一条指令，每行一条，不要解释；无重复则只输出 `NONE`。\n\n" +
		"记忆条目：\n" + sb.String()
	resp, err := client.Chat(ctx, llm.ChatRequest{
		Model: modelName,
		Messages: []llm.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil || resp == nil {
		return err
	}
	dropIDs, merges := parseConsolidation(resp.Delta, memories)
	// 创建合并后的新记忆（含 embedding）
	now := time.Now().Unix()
	for _, mg := range merges {
		m := &model.TeamMemory{
			ID:        generateID("tm"),
			TeamID:    teamID,
			UserID:    s.memoryUserID(memories, mg.sourceIDs),
			Content:   mg.content,
			Status:    "active",
			LastHitAt: now,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.repo.Create(m); err != nil {
			continue
		}
		if s.embedder != nil && s.embeddingModel != "" {
			if vecs, e := s.embedder.Embed(ctx, s.embeddingModel, []string{mg.content}); e == nil && len(vecs) > 0 && len(vecs[0]) > 0 {
				_ = s.repo.UpdateEmbedding(m.ID, llm.EncodeFloat32Vector(vecs[0]))
			}
		}
	}
	// 标记被取代条目（DROP + MERGE 源）
	superseded := dropIDs
	for _, mg := range merges {
		superseded = append(superseded, mg.sourceIDs...)
	}
	if len(superseded) > 0 {
		_ = s.repo.MarkSuperseded(superseded)
	}
	return nil
}

// Forget 归档长期未命中的 active 记忆（实现 MemoryStore.Forget，AR-09）。
// created_at 与 last_hit_at 均早于 TTL（默认 90 天）的条目标记 archived。
// ctx 预留给后续异步/取消扩展，当前归档为单次 DB 更新。
func (s *TeamMemoryService) Forget(_ context.Context, teamID string) error {
	if teamID == "" {
		return nil
	}
	cutoff := time.Now().Unix() - int64(teamMemoryForgetTTLDays)*86400
	return s.repo.ArchiveStale(teamID, cutoff)
}

// consolidateMerge 表示一次 MERGE 指令：合并后的内容 + 被取代的源条目 ID
type consolidateMerge struct {
	content   string
	sourceIDs []string
}

// parseConsolidation 解析 LLM 合并指令（DROP/MERGE），容错跳过无法解析的行。
// memories 按 1..N 编号对应 LLM 输入顺序。
func parseConsolidation(text string, memories []model.TeamMemory) (dropIDs []string, merges []consolidateMerge) {
	byIdx := func(n int) *model.TeamMemory {
		if n >= 1 && n <= len(memories) {
			return &memories[n-1]
		}
		return nil
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "NONE" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "DROP "):
			for _, s := range strings.Split(strings.TrimPrefix(line, "DROP "), ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					if m := byIdx(n); m != nil {
						dropIDs = append(dropIDs, m.ID)
					}
				}
			}
		case strings.HasPrefix(line, "MERGE "):
			rest := strings.TrimSpace(strings.TrimPrefix(line, "MERGE "))
			pipe := strings.Index(rest, "|")
			if pipe < 0 {
				continue
			}
			idxPart := strings.TrimSpace(rest[:pipe])
			content := strings.TrimSpace(rest[pipe+1:])
			if content == "" {
				continue
			}
			var srcIDs []string
			for _, s := range strings.Split(idxPart, ",") {
				if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
					if m := byIdx(n); m != nil {
						srcIDs = append(srcIDs, m.ID)
					}
				}
			}
			if len(srcIDs) > 0 {
				merges = append(merges, consolidateMerge{content: content, sourceIDs: srcIDs})
			}
		}
	}
	return
}

// memoryUserID 取记忆列表中给定 ID 的 UserID（合并新记忆归属沿用首个源条目的用户）
func (s *TeamMemoryService) memoryUserID(memories []model.TeamMemory, ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	for _, m := range memories {
		if m.ID == ids[0] {
			return m.UserID
		}
	}
	return ""
}
