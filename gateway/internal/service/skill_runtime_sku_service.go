package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"go.uber.org/zap"
)

// SkillRuntimeSKUService 据 MCP tools/list 自动派生可购买 SKU。
//
// 当 SkillRuntime.AutoSKU=true 时，supervisor/探活成功拿到 []MCPTool 后调用 DeriveSKUs：
// 每个 tool 合成一份 ToolManifest 并 upsert 为 AgentItem（status=approved、官方、免费），
// 免去在 marketplace/<mod>/skus/ 下手写 SKU 文件。消失的工具 -> 下架（delisted，保留购买记录）。
//
// 对标 openhuman tool_registry/ops.rs 的「从 tools/list 合成注册项」，但更进一步：
// openhuman 只做只读发现元数据，这里合成的是可购买可调用的 SKU（AgentItem+ToolManifest）。
//
// 调用链不变：LLM -> AgentToolLoader（按 manifest.driver=rt.DriverID 命中 SkillRuntimeDriver 别名）
// -> SkillRuntimeDriver.Execute -> SkillRuntimeRegistry.Execute（按 transport 分发）。
// 派生 SKU 的 manifest.Metadata.module=rt.ID 使 AgentToolLoader 的在线门控生效。
type SkillRuntimeSKUService struct {
	repo   *repository.AgentRepo
	logger *zap.Logger
	mu     sync.Mutex
	last   map[string]string // runtime_id -> tools 签名（sha256），签名未变则跳过，避免每轮探活重复写库
}

// NewSkillRuntimeSKUService 创建自动 SKU 派生服务
func NewSkillRuntimeSKUService(repo *repository.AgentRepo, logger *zap.Logger) *SkillRuntimeSKUService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &SkillRuntimeSKUService{
		repo:   repo,
		logger: logger,
		last:   make(map[string]string),
	}
}

// FilterTools 按 SkillRuntime 的 allowed_tools/disallowed_tools 过滤 MCP 工具列表。
// allowed_tools 非空时仅保留白名单内工具；disallowed_tools 始终排除（黑名单优先于白名单）。
// 对标 openhuman apply_safety_filter：探活时过滤 tools/list，使 DeriveSKUs 只为允许的工具出 SKU，
// 同时 capabilities/status 也只反映允许的工具。无配置时原样返回。
func FilterTools(rt *model.SkillRuntime, tools []MCPTool) []MCPTool {
	if rt == nil || len(tools) == 0 {
		return tools
	}
	allowed := rt.AllowedToolsList()
	disallowed := rt.DisallowedToolsList()
	if len(allowed) == 0 && len(disallowed) == 0 {
		return tools
	}
	disallowedSet := make(map[string]bool, len(disallowed))
	for _, n := range disallowed {
		disallowedSet[n] = true
	}
	allowedSet := make(map[string]bool, len(allowed))
	for _, n := range allowed {
		allowedSet[n] = true
	}
	filtered := make([]MCPTool, 0, len(tools))
	for _, t := range tools {
		if disallowedSet[t.Name] {
			continue
		}
		if len(allowedSet) > 0 && !allowedSet[t.Name] {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// DeriveSKUs 根据 tools/list 结果为 auto_sku 运行时同步可购买 SKU。
// 幂等：工具集签名（含 InputSchema）未变时直接跳过；失败不更新签名缓存，下次探活重试。
// 空工具列表视为探活异常，跳过以免误下架全部 SKU。
func (s *SkillRuntimeSKUService) DeriveSKUs(rt *model.SkillRuntime, tools []MCPTool) {
	if rt == nil || !rt.AutoSKU || s.repo == nil || rt.DriverID == "" || len(tools) == 0 {
		return
	}
	sig := toolsSignature(tools)

	// 全程持锁：派生是「读旧->算 diff->写新/改状态」的复合操作，串行化避免并发 Create 主键冲突。
	// 探活周期 60s/5min，派生仅在工具集变化时触发，持锁耗时毫秒级，无 contention 顾虑。
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last[rt.ID] == sig {
		return
	}
	if err := s.deriveAndSync(rt, tools); err != nil {
		if s.logger != nil {
			s.logger.Warn("自动派生 SKU 失败",
				zap.String("runtime_id", rt.ID),
				zap.Error(err))
		}
		return
	}
	s.last[rt.ID] = sig
	if s.logger != nil {
		s.logger.Info("自动派生 SKU 完成",
			zap.String("runtime_id", rt.ID),
			zap.String("driver_id", rt.DriverID),
			zap.Int("tools", len(tools)))
	}
}

// deriveAndSync 读现有 SKU -> upsert 当前 tools -> 下架消失工具。
func (s *SkillRuntimeSKUService) deriveAndSync(rt *model.SkillRuntime, tools []MCPTool) error {
	const adminID = "00000000-0000-0000-0000-000000000000"
	now := time.Now()

	existing, err := s.repo.ListByModuleSKUs(rt.ID)
	if err != nil {
		return fmt.Errorf("查询模块 %s 现有 SKU 失败: %w", rt.ID, err)
	}
	existingMap := make(map[string]*model.AgentItem, len(existing))
	for _, it := range existing {
		existingMap[it.ID] = it
	}

	seen := make(map[string]bool, len(tools))
	for _, t := range tools {
		skuID := moduleSKUID(rt.ID, t.Name)
		seen[skuID] = true

		manifest := buildDerivedManifest(rt, t)
		mfJSON, err := json.Marshal(manifest)
		if err != nil {
			continue
		}
		mfStr := string(mfJSON)

		if item, ok := existingMap[skuID]; ok {
			// 已存在：同步 manifest/名称/描述；保留 purchase_count/avg_rating 等统计与购买记录。
			// 重新置 approved（工具曾消失被 delist，如今再次出现 -> 重新上架）。
			changed := item.ManifestJSON != mfStr ||
				item.Name != manifest.Name ||
				item.Description != manifest.Description ||
				item.Status != model.AgentStatusApproved
			if !changed {
				continue
			}
			item.ManifestJSON = mfStr
			item.Name = manifest.Name
			item.Description = manifest.Description
			item.Category = manifest.Category
			item.Status = model.AgentStatusApproved
			if err := s.repo.Update(item); err != nil {
				return fmt.Errorf("更新自动 SKU %s 失败: %w", skuID, err)
			}
		} else {
			item := &model.AgentItem{
				ID:           skuID,
				Name:         manifest.Name,
				Description:  manifest.Description,
				Category:     manifest.Category,
				Level:        model.AgentLevel(manifest.Level),
				PriceDanwan:  manifest.PriceDanwan,
				ManifestJSON: mfStr,
				Status:       model.AgentStatusApproved,
				CreatorID:    adminID,
				CreatorName:  "官方",
				CreatedAt:    now,
			}
			if err := s.repo.Create(item); err != nil {
				return fmt.Errorf("创建自动 SKU %s 失败: %w", skuID, err)
			}
		}
	}

	// 消失的工具 -> 下架（delisted，保留购买记录不直接删）。
	// 仅处理本服务派生的 SKU（manifest 标记 auto_sku_module == rt.ID），避免误伤同前缀的手写 SKU。
	for skuID, item := range existingMap {
		if seen[skuID] {
			continue
		}
		mf, _ := item.Manifest()
		if mf == nil || mf.Metadata["auto_sku_module"] != rt.ID {
			continue
		}
		if item.Status == model.AgentStatusApproved {
			if err := s.repo.UpdateStatus(skuID, model.AgentStatusDelisted); err != nil {
				return fmt.Errorf("下架消失工具 SKU %s 失败: %w", skuID, err)
			}
		}
	}
	return nil
}

// buildDerivedManifest 据 MCPTool + SkillRuntime 合成 ToolManifest。
// - Driver=rt.DriverID：命中 SkillRuntimeDriver 别名，Execute 时 resolveRuntimeID 经 GetByDriverID 定位运行时。
// - Metadata.module=rt.ID：AgentToolLoader 的在线门控（模块离线则不暴露工具）。
// - Metadata.auto_sku_module=rt.ID：diff 时精确识别本服务派生的 SKU。
// - Parameters=tool.InputSchema 透传（MCP JSON Schema 即 OpenAI function parameters）。
// - Actions=[{Name:tool.Name}]：buildToolFunc 取首个 action 作为 tools/call 的 name。
// - Credentials=rt.CredentialsMap()：从 module.json 透传，供 web 提示用户填写；env 模板 ${credentials.KEY} 引用同名 key。
func buildDerivedManifest(rt *model.SkillRuntime, t MCPTool) model.ToolManifest {
	name := t.Name
	desc := t.Description
	if desc == "" {
		desc = t.Name
	}

	params := t.InputSchema
	if params == nil {
		params = map[string]interface{}{}
	}
	if _, ok := params["type"].(string); !ok {
		params["type"] = "object"
	}

	runtimeType := "remote"
	if rt.Deployment == model.SkillRuntimeDeploymentProcess {
		runtimeType = "sidecar"
	}

	metadata := map[string]string{
		"module":          rt.ID,
		"auto_sku_module": rt.ID,
	}
	// M5：伪工具标注（read_resource/get_prompt 由协议层据 resources/prompts capability 合成），
	// 供 UI 区分展示「资源读取器/提示获取器」而非普通工具。
	switch t.Name {
	case mcpPseudoToolReadResource:
		metadata["pseudo_tool"] = "resource"
	case mcpPseudoToolGetPrompt:
		metadata["pseudo_tool"] = "prompt"
	}

	return model.ToolManifest{
		ID:          moduleSKUID(rt.ID, t.Name),
		Name:        name,
		Description: desc,
		Driver:      model.ToolDriverType(rt.DriverID),
		RuntimeType: runtimeType,
		Category:    rt.Name,
		Level:       int(model.AgentLevelHuang),
		PriceDanwan: 0,
		Parameters:  params,
		Actions:     []model.ToolAction{{Name: t.Name, Description: desc}},
		Metadata:    metadata,
		Credentials: rt.CredentialsMap(),
	}
}

// toolsSignature 计算工具集签名：按 name 排序后整体 marshal 再 sha256。
// json.Marshal 对 map key 按字典序输出，故 InputSchema 变化也会反映到签名，触发重新派生。
func toolsSignature(tools []MCPTool) string {
	sorted := make([]MCPTool, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	b, err := json.Marshal(sorted)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// moduleSKUID 自动派生 SKU 的 AgentItem.ID 约定：<runtimeID>-<toolName>。
// 与手写 SKU 的 "{module}-{sku_file}" 同前缀（ListByModuleSKUs 据此粗筛），
// 故 diff 须再据 manifest.auto_sku_module 精确判定归属。
func moduleSKUID(runtimeID, toolName string) string {
	return runtimeID + "-" + sanitizeSKUToolName(toolName)
}

// sanitizeSKUToolName 收敛工具名为 SKU ID 安全字符（[a-zA-Z0-9_-]，其余转 _）。
func sanitizeSKUToolName(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if s == "" {
		return "tool"
	}
	return s
}
