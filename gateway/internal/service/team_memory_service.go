package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
)

// TeamMemoryService 组共享记忆业务逻辑（Agent Team P2）。
// 检索实现集中在 SearchForInjection，与调用方隔离：当前为「关键词 LIKE 命中加权 + 时间衰减」
// 的从简方案，后续可替换为 embedding 检索（EleAgent 模型中心具备 embedding 能力时）而不动调用方。
type TeamMemoryService struct {
	repo *repository.TeamMemoryRepo
	team *TeamService
}

// NewTeamMemoryService 创建服务
func NewTeamMemoryService(repo *repository.TeamMemoryRepo, team *TeamService) *TeamMemoryService {
	return &TeamMemoryService{repo: repo, team: team}
}

// SearchForInjection 组内检索用于 system prompt 注入的记忆：
// 关键词 LIKE 命中加权（Content 命中 +2 / Tags 命中 +3）叠加时间衰减（30 天半衰），
// 按相关度排序取 topN；query 为空时退化为按时间倒序取 topN。
// 组归属校验失败或检索出错时返回空切片（注入是增强项，不阻断主流程）。
func (s *TeamMemoryService) SearchForInjection(ctx context.Context, userID, teamID, query string, topN int) []model.TeamMemory {
	if topN <= 0 {
		topN = 8
	}
	if err := s.team.CheckOwned(userID, teamID); err != nil {
		return []model.TeamMemory{}
	}
	candidates, err := s.repo.ListRecent(teamID, teamMemoryCandidateLimit)
	if err != nil || len(candidates) == 0 {
		return []model.TeamMemory{}
	}

	tokens := tokenizeMemoryQuery(query)
	// 无有效关键词：直接按时间倒序取 topN（ListRecent 已倒序）
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
		if err := s.repo.Create(&model.TeamMemory{
			ID:                   generateID("tm"),
			TeamID:               teamID,
			UserID:               userID,
			Content:              item,
			SourceConversationID: conversationID,
			CreatedAt:            now,
			UpdatedAt:            now,
		}); err != nil {
			return err
		}
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
