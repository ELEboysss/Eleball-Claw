package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/pkg/crypto"
	"github.com/eleball/gateway/pkg/util"
	"github.com/google/uuid"
)

// EleAgentModelService Ele Agent 模型配置服务
// 负责管理员配置的 CRUD、客户端选项下发、后端调用凭据解密。
type EleAgentModelService struct {
	repo    *repository.EleAgentModelRepo
	encrypt *crypto.KeyEncryption

	// cloudAPIBase 云端 API Base（如 https://api.eleball.cn/v1）。
	// 指向该地址的配置视为「云端代理」：调用时自动使用请求方的当前云端登录态作为凭证，
	// 存储的 API Key 仅作无请求上下文场景（如后台任务恢复）的兜底，免于登录态过期后手动换 Key。
	cloudAPIBase string

	mu    sync.RWMutex
	items []*model.EleAgentModelConfig // 内存缓存，按 provider/model 分组
}

// SetCloudAPIBase 设置云端 API Base（用于识别云端代理配置）
func (s *EleAgentModelService) SetCloudAPIBase(base string) {
	s.cloudAPIBase = strings.TrimSuffix(strings.TrimSpace(base), "/")
}

// EleAgentModelCredential Ele Agent 后端调用凭据
type EleAgentModelCredential struct {
	Provider  string
	Protocol  string
	ModelName string
	BaseURL   string
	APIKey    string
	// CloudProxy 为 true 表示该配置指向云端网关（cloudAPIBase）：
	// 云端非流式响应为私有信封格式（{code,data:{delta}}），通用 OpenAI 客户端无法解析，
	// 调用方应使用 cloudEnvelopeCompatClient 包装（Chat 聚合 SSE，见 cloud_proxy_client.go）。
	CloudProxy bool
}

// NewNoOpEleAgentModelService 创建一个不管理任何配置的 EleAgentModelService，用于测试。
func NewNoOpEleAgentModelService() *EleAgentModelService {
	return &EleAgentModelService{
		repo:  nil,
		items: make([]*model.EleAgentModelConfig, 0),
	}
}

// NewTestEleAgentModelServiceWithConfigs 创建带有指定内存配置的 EleAgentModelService，仅用于测试。
func NewTestEleAgentModelServiceWithConfigs(configs []*model.EleAgentModelConfig) *EleAgentModelService {
	return &EleAgentModelService{
		repo:  nil,
		items: configs,
	}
}

// NewEleAgentModelService 创建 Ele Agent 模型配置服务
func NewEleAgentModelService(repo *repository.EleAgentModelRepo, masterKeyHex string) (*EleAgentModelService, error) {
	if repo == nil {
		return NewNoOpEleAgentModelService(), nil
	}
	svc := &EleAgentModelService{
		repo:  repo,
		items: make([]*model.EleAgentModelConfig, 0),
	}

	if masterKeyHex != "" {
		ke, err := crypto.NewKeyEncryption(masterKeyHex)
		if err != nil {
			return nil, fmt.Errorf("初始化 EleAgentModel KeyEncryption 失败: %w", err)
		}
		svc.encrypt = ke
	}

	if err := svc.reload(); err != nil {
		return nil, fmt.Errorf("加载 Ele Agent 模型配置失败: %w", err)
	}

	// 后台定时刷新（每 30 秒）
	go svc.backgroundReload()

	return svc, nil
}

// ListOptions 返回客户端可选择的平台-模型选项
func (s *EleAgentModelService) ListOptions() []*model.EleAgentModelOption {
	s.mu.RLock()
	defer s.mu.RUnlock()

	options := make([]*model.EleAgentModelOption, 0, len(s.items))
	seen := make(map[string]bool)
	for _, item := range s.items {
		if !item.IsEnabled {
			continue
		}
		key := item.Provider + "/" + item.ModelName
		if seen[key] {
			continue
		}
		seen[key] = true
		display := item.DisplayName
		if display == "" {
			display = fmt.Sprintf("%s/%s", item.Provider, item.ModelName)
		}
		options = append(options, &model.EleAgentModelOption{
			Provider:                  item.Provider,
			ModelName:                 item.ModelName,
			DisplayName:               display,
			Protocol:                  item.Protocol,
			SupportsChat:              item.SupportsChat,
			SupportsVision:            item.SupportsVision,
			SupportsImage:             item.SupportsImage,
			SupportsVideo:             item.SupportsVideo,
			SupportsImageInput:        item.SupportsImageInput,
			SupportsContinuousContext: item.SupportsContinuousContext,
			SupportsTools:             item.SupportsTools,
			CloudProxy:                s.cloudAPIBase != "" && strings.TrimSuffix(item.BaseURL, "/") == s.cloudAPIBase,
			InputPricePerCall:         item.InputPricePerCall,
			PricePerCall:              item.PricePerCall,
			PricePerGeneration:        item.PricePerGeneration,
			Priority:                  item.Priority,
			VideoMinDuration:          item.VideoMinDuration,
			VideoMaxDuration:          item.VideoMaxDuration,
			VideoDurationStep:         item.VideoDurationStep,
		})
	}

	// 按优先级升序排序；优先级相同则按模型名稳定排序
	sort.Slice(options, func(i, j int) bool {
		if options[i].Priority != options[j].Priority {
			return options[i].Priority < options[j].Priority
		}
		return options[i].ModelName < options[j].ModelName
	})

	return options
}

// HasModel 判断指定平台与模型名是否已配置且启用
func (s *EleAgentModelService) HasModel(provider, modelName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return true
		}
	}
	return false
}

// GetModelPrices 查询指定平台与模型的输入/输出 token 单价（弹丸 / 1M tokens）
// 未找到或两者均为 0 时视为免费。
func (s *EleAgentModelService) GetModelPrices(provider, modelName string) (inputPrice, outputPrice int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.InputPricePerCall, item.PricePerCall
		}
	}
	return 0, 0
}

// GetModelPricing 查询指定平台与模型的完整定价信息
// 返回：输入 token 单价、输出 token 单价、按次附加费（与 token 费用相加）
func (s *EleAgentModelService) GetModelPricing(provider, modelName string) (inputPrice, outputPrice, perGenPrice int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.InputPricePerCall, item.PricePerCall, item.PricePerGeneration
		}
	}
	return 0, 0, 0
}

// GetVideoDurationLimits 查询指定平台与模型的视频时长限制（单位秒）。
// 返回的 maxDuration 为 0 表示未配置上限，由前端自由输入。
func (s *EleAgentModelService) GetVideoDurationLimits(provider, modelName string) (minDuration, maxDuration, step int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.VideoMinDuration, item.VideoMaxDuration, item.VideoDurationStep
		}
	}
	return 0, 0, 0
}

// GetModelCapability 查询指定平台与模型是否支持视觉理解
func (s *EleAgentModelService) GetModelCapability(provider, modelName string) (supportsVision bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.SupportsVision
		}
	}
	return false
}

// GetModelMediaCapabilities 查询指定平台与模型是否支持图片/视频生成
func (s *EleAgentModelService) GetModelMediaCapabilities(provider, modelName string) (supportsImage, supportsVideo bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.SupportsImage, item.SupportsVideo
		}
	}
	return false, false
}

// GetModelToolSupport 查询指定平台与模型是否支持函数/工具调用
func (s *EleAgentModelService) GetModelToolSupport(provider, modelName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.SupportsTools
		}
	}
	return false
}

// GetModelChatSupport 查询指定平台与模型是否支持文字对话。
// 纯图片/纯视频生成模型返回 false，对话链路据此拦截不可用模型。
func (s *EleAgentModelService) GetModelChatSupport(provider, modelName string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.items {
		if item.IsEnabled && item.Provider == provider && item.ModelName == modelName {
			return item.SupportsChat
		}
	}
	return false
}

// GetCredential 根据平台与模型名获取后端调用凭据
func (s *EleAgentModelService) GetCredential(provider, modelName string) (*EleAgentModelCredential, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched *model.EleAgentModelConfig
	for _, item := range s.items {
		if !item.IsEnabled {
			continue
		}
		if item.Provider == provider && item.ModelName == modelName {
			if matched != nil {
				return nil, fmt.Errorf("存在多条启用的 %s/%s 模型配置（protocol: %s 与 %s），请在管理后台删除重复配置",
					provider, modelName, matched.Protocol, item.Protocol)
			}
			matched = item
		}
	}
	if matched == nil {
		return nil, fmt.Errorf("未找到 Ele Agent 模型配置: %s/%s", provider, modelName)
	}

	apiKey, err := s.decryptKey(matched)
	if err != nil {
		return nil, fmt.Errorf("解密 Ele Agent 模型 API Key 失败: %w", err)
	}
	return &EleAgentModelCredential{
		Provider:  matched.Provider,
		Protocol:  string(matched.Protocol),
		ModelName: matched.ModelName,
		BaseURL:   matched.BaseURL,
		APIKey:    apiKey,
	}, nil
}

// EleAgentModelConfigInput 创建模型配置的参数
type EleAgentModelConfigInput struct {
	Provider                  string
	Protocol                  string
	ModelName                 string
	DisplayName               string
	BaseURL                   string
	APIKey                    string
	Priority                  int
	InputPricePerCall         int64
	PricePerCall              int64
	PricePerGeneration        int64 // 按次附加费（弹丸/次），与 token 费用相加，适用于对话/图片/视频模型
	VideoMinDuration          int   // 视频最小时长（秒），0 表示不限制
	VideoMaxDuration          int   // 视频最大时长（秒），0 表示不限制
	VideoDurationStep         int   // 视频时长步长（秒）
	SupportsChat              bool
	SupportsVision            bool
	SupportsImage             bool
	SupportsVideo             bool
	SupportsImageInput        bool
	SupportsContinuousContext bool
	SupportsTools             bool
}

// CreateConfig 创建配置
func (s *EleAgentModelService) CreateConfig(in EleAgentModelConfigInput) (*model.EleAgentModelListItem, error) {
	if in.Provider == "" || in.ModelName == "" || in.BaseURL == "" || in.APIKey == "" {
		return nil, errors.New("平台、模型名、BaseURL、API Key 不能为空")
	}
	// 协议字段兜底，兼容旧客户端/旧数据
	protocol := in.Protocol
	if protocol == "" {
		protocol = string(model.EleAgentUpstreamOpenAICompatible)
	}

	// 协议与能力必须一致（对话/图片/视频至少一项，视觉生成协议与媒体能力匹配），
	// 避免前端选到视频模型实际配成图片协议、或纯视觉模型被选来发起文字对话
	if err := validateProtocolCapabilities(protocol, in.SupportsChat, in.SupportsImage, in.SupportsVideo); err != nil {
		return nil, err
	}

	// 同一 provider/model 不允许重复启用配置，否则运行时无法确定使用哪条协议
	if s.hasActiveDuplicate(in.Provider, in.ModelName, "") {
		return nil, fmt.Errorf("已存在启用的 %s/%s 模型配置，请勿重复添加", in.Provider, in.ModelName)
	}

	if s.encrypt == nil {
		return nil, errors.New("未配置 ENCRYPTION_MASTER_KEY，无法加密存储 API Key")
	}

	ciphertext, nonce, version, err := s.encrypt.Encrypt(in.APIKey)
	if err != nil {
		return nil, fmt.Errorf("加密 API Key 失败: %w", err)
	}

	config := &model.EleAgentModelConfig{
		ID:                        uuid.New().String(),
		Provider:                  in.Provider,
		Protocol:                  model.EleAgentUpstreamProtocol(protocol),
		ModelName:                 in.ModelName,
		DisplayName:               in.DisplayName,
		BaseURL:                   in.BaseURL,
		EncryptedKey:              ciphertext,
		Nonce:                     nonce,
		KeyVersion:                version,
		IsEnabled:                 true,
		SupportsChat:              in.SupportsChat,
		SupportsVision:            in.SupportsVision,
		SupportsImage:             in.SupportsImage,
		SupportsVideo:             in.SupportsVideo,
		SupportsImageInput:        in.SupportsImageInput,
		SupportsContinuousContext: in.SupportsContinuousContext,
		SupportsTools:             in.SupportsTools,
		Priority:                  in.Priority,
		InputPricePerCall:         in.InputPricePerCall,
		PricePerCall:              in.PricePerCall,
		PricePerGeneration:        in.PricePerGeneration,
		VideoMinDuration:          in.VideoMinDuration,
		VideoMaxDuration:          in.VideoMaxDuration,
		VideoDurationStep:         in.VideoDurationStep,
	}

	if err := s.repo.Create(config); err != nil {
		return nil, err
	}

	_ = s.reload()
	return toEleAgentModelListItem(config), nil
}

// EleAgentModelConfigPatch 更新模型配置的可选字段。
// 字符串字段为空表示不更新；指针字段为 nil 表示不更新。
type EleAgentModelConfigPatch struct {
	Provider                  string
	Protocol                  string
	ModelName                 string
	DisplayName               string
	BaseURL                   string
	IsEnabled                 *bool
	SupportsChat              *bool
	SupportsVision            *bool
	SupportsImage             *bool
	SupportsVideo             *bool
	SupportsImageInput        *bool
	SupportsContinuousContext *bool
	SupportsTools             *bool
	Priority                  *int
	InputPricePerCall         *int64
	PricePerCall              *int64
	PricePerGeneration        *int64
	VideoMinDuration          *int
	VideoMaxDuration          *int
	VideoDurationStep         *int
}

// UpdateConfig 更新配置元信息
func (s *EleAgentModelService) UpdateConfig(id string, patch EleAgentModelConfigPatch) (*model.EleAgentModelListItem, error) {
	existing, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	updates := map[string]interface{}{
		"provider":                    existing.Provider,
		"protocol":                    existing.Protocol,
		"model_name":                  existing.ModelName,
		"display_name":                existing.DisplayName,
		"base_url":                    existing.BaseURL,
		"is_enabled":                  existing.IsEnabled,
		"supports_chat":               existing.SupportsChat,
		"supports_vision":             existing.SupportsVision,
		"supports_image":              existing.SupportsImage,
		"supports_video":              existing.SupportsVideo,
		"supports_image_input":        existing.SupportsImageInput,
		"supports_continuous_context": existing.SupportsContinuousContext,
		"supports_tools":              existing.SupportsTools,
		"priority":                    existing.Priority,
		"input_price_per_call":        existing.InputPricePerCall,
		"price_per_call":              existing.PricePerCall,
		"price_per_generation":        existing.PricePerGeneration,
		"video_min_duration":          existing.VideoMinDuration,
		"video_max_duration":          existing.VideoMaxDuration,
		"video_duration_step":         existing.VideoDurationStep,
	}
	if patch.Provider != "" {
		updates["provider"] = patch.Provider
	}
	if patch.Protocol != "" {
		updates["protocol"] = model.EleAgentUpstreamProtocol(patch.Protocol)
	}
	if patch.ModelName != "" {
		updates["model_name"] = patch.ModelName
	}
	if patch.DisplayName != "" {
		updates["display_name"] = patch.DisplayName
	}
	if patch.BaseURL != "" {
		updates["base_url"] = patch.BaseURL
	}
	if patch.IsEnabled != nil {
		updates["is_enabled"] = *patch.IsEnabled
	}
	if patch.SupportsChat != nil {
		updates["supports_chat"] = *patch.SupportsChat
	}
	if patch.SupportsVision != nil {
		updates["supports_vision"] = *patch.SupportsVision
	}
	if patch.SupportsImage != nil {
		updates["supports_image"] = *patch.SupportsImage
	}
	if patch.SupportsVideo != nil {
		updates["supports_video"] = *patch.SupportsVideo
	}
	if patch.SupportsImageInput != nil {
		updates["supports_image_input"] = *patch.SupportsImageInput
	}
	if patch.SupportsContinuousContext != nil {
		updates["supports_continuous_context"] = *patch.SupportsContinuousContext
	}
	if patch.SupportsTools != nil {
		updates["supports_tools"] = *patch.SupportsTools
	}
	if patch.Priority != nil {
		updates["priority"] = *patch.Priority
	}
	if patch.InputPricePerCall != nil {
		updates["input_price_per_call"] = *patch.InputPricePerCall
	}
	if patch.PricePerCall != nil {
		updates["price_per_call"] = *patch.PricePerCall
	}
	if patch.PricePerGeneration != nil {
		updates["price_per_generation"] = *patch.PricePerGeneration
	}
	if patch.VideoMinDuration != nil {
		updates["video_min_duration"] = *patch.VideoMinDuration
	}
	if patch.VideoMaxDuration != nil {
		updates["video_max_duration"] = *patch.VideoMaxDuration
	}
	if patch.VideoDurationStep != nil {
		updates["video_duration_step"] = *patch.VideoDurationStep
	}

	// 协议与能力必须一致（对话/图片/视频至少一项，视觉生成协议与媒体能力匹配）
	if err := validateProtocolCapabilities(
		string(updates["protocol"].(model.EleAgentUpstreamProtocol)),
		updates["supports_chat"].(bool),
		updates["supports_image"].(bool),
		updates["supports_video"].(bool),
	); err != nil {
		return nil, err
	}

	// 更新后不允许与另一条启用配置产生 provider/model 重复
	finalProvider := updates["provider"].(string)
	finalModelName := updates["model_name"].(string)
	if s.hasActiveDuplicate(finalProvider, finalModelName, id) {
		return nil, fmt.Errorf("已存在启用的 %s/%s 模型配置，请勿重复", finalProvider, finalModelName)
	}

	if err := s.repo.UpdateFields(id, updates); err != nil {
		return nil, err
	}
	_ = s.reload()

	updated, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return toEleAgentModelListItem(updated), nil
}

// RotateAPIKey 轮换 API Key
func (s *EleAgentModelService) RotateAPIKey(id, newAPIKey string) (*model.EleAgentModelListItem, error) {
	if s.encrypt == nil {
		return nil, errors.New("未配置 ENCRYPTION_MASTER_KEY，无法加密存储 API Key")
	}

	config, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}

	ciphertext, nonce, version, err := s.encrypt.Encrypt(newAPIKey)
	if err != nil {
		return nil, fmt.Errorf("加密 API Key 失败: %w", err)
	}

	config.EncryptedKey = ciphertext
	config.Nonce = nonce
	config.KeyVersion = version
	if err := s.repo.Update(config); err != nil {
		return nil, err
	}

	_ = s.reload()
	return toEleAgentModelListItem(config), nil
}

// DeleteConfig 删除配置
func (s *EleAgentModelService) DeleteConfig(id string) error {
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	return s.reload()
}

// GetConfig 获取单个配置
func (s *EleAgentModelService) GetConfig(id string) (*model.EleAgentModelListItem, error) {
	config, err := s.repo.GetByID(id)
	if err != nil {
		return nil, err
	}
	return toEleAgentModelListItem(config), nil
}

// ListConfigs 列表查询
func (s *EleAgentModelService) ListConfigs(provider string, page, pageSize int) ([]*model.EleAgentModelListItem, int64, error) {
	items, total, err := s.repo.List(provider, page, pageSize)
	if err != nil {
		return nil, 0, err
	}

	result := make([]*model.EleAgentModelListItem, len(items))
	for i, item := range items {
		result[i] = toEleAgentModelListItem(item)
	}
	return result, total, nil
}

// GetCredentialForRequest 按请求上下文获取调用凭据。
// 若配置指向云端（cloudAPIBase），且请求携带了有效的调用方 token（统一云端账户登录态），
// 则用该 token 覆盖存储的 API Key —— 登录态由前端自动续期，代理调用不再因存储 token 过期而失效。
// 无上下文 token（后台任务、CLI 静态密钥调用）时回退到存储的 API Key。
func (s *EleAgentModelService) GetCredentialForRequest(ctx context.Context, provider, modelName string) (*EleAgentModelCredential, error) {
	cred, err := s.GetCredential(provider, modelName)
	if err != nil {
		return nil, err
	}
	if s.cloudAPIBase != "" && strings.TrimSuffix(cred.BaseURL, "/") == s.cloudAPIBase {
		cred.CloudProxy = true
		if token := util.AuthTokenFrom(ctx); token != "" {
			cred.APIKey = token
		}
	}
	return cred, nil
}

// decryptKey 解密单个 Key
func (s *EleAgentModelService) decryptKey(config *model.EleAgentModelConfig) (string, error) {
	if s.encrypt == nil {
		return "", errors.New("未配置 ENCRYPTION_MASTER_KEY，无法解密 API Key")
	}
	return s.encrypt.Decrypt(config.EncryptedKey, config.Nonce)
}

// reload 从数据库重新加载配置到内存
func (s *EleAgentModelService) reload() error {
	items, err := s.repo.ListEnabled()
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.items = items
	s.mu.Unlock()
	return nil
}

// backgroundReload 后台定时刷新
func (s *EleAgentModelService) backgroundReload() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		_ = s.reload()
	}
}

// hasActiveDuplicate 检查是否存在另一条启用的同 provider/model 配置。
// excludeID 用于更新场景排除自身。
func (s *EleAgentModelService) hasActiveDuplicate(provider, modelName, excludeID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if !item.IsEnabled {
			continue
		}
		if item.ID == excludeID {
			continue
		}
		if item.Provider == provider && item.ModelName == modelName {
			return true
		}
	}
	return false
}

// ============================================================================
//  批量导出 / 导入
// ============================================================================

// EleAgentModelExportItem 单条模型配置的导出/导入结构。
// APIKey 仅在导出勾选包含密钥、或导入需要新建/换 Key 时出现。
// Present 记录导入 JSON 中实际出现的字段（不参与序列化）：
// 更新已有配置时只覆盖出现的字段，未出现的字段保持原值，避免手工精简文件把配置洗坏。
type EleAgentModelExportItem struct {
	Provider                  string `json:"provider"`
	Protocol                  string `json:"protocol"`
	ModelName                 string `json:"model_name"`
	DisplayName               string `json:"display_name,omitempty"`
	BaseURL                   string `json:"base_url"`
	APIKey                    string `json:"api_key,omitempty"`
	IsEnabled                 *bool  `json:"is_enabled,omitempty"`
	SupportsChat              bool   `json:"supports_chat"`
	SupportsVision            bool   `json:"supports_vision"`
	SupportsImage             bool   `json:"supports_image"`
	SupportsVideo             bool   `json:"supports_video"`
	SupportsImageInput        bool   `json:"supports_image_input"`
	SupportsContinuousContext bool   `json:"supports_continuous_context"`
	SupportsTools             bool   `json:"supports_tools"`
	Priority                  int    `json:"priority"`
	InputPricePerCall         int64  `json:"input_price_per_call"`
	PricePerCall              int64  `json:"price_per_call"`
	PricePerGeneration        int64  `json:"price_per_generation"`
	VideoMinDuration          int    `json:"video_min_duration"`
	VideoMaxDuration          int    `json:"video_max_duration"`
	VideoDurationStep         int    `json:"video_duration_step"`

	Present map[string]bool `json:"-"`
}

// UnmarshalJSON 自定义解码：在解析字段值的同时记录 JSON 中出现了哪些字段。
func (item *EleAgentModelExportItem) UnmarshalJSON(data []byte) error {
	type alias EleAgentModelExportItem
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*item = EleAgentModelExportItem(a)
	item.Present = make(map[string]bool, len(raw))
	for k := range raw {
		item.Present[k] = true
	}
	return nil
}

// Has 判断导入项是否提供了指定字段。
// 经 JSON 解码时以文件中实际出现的字段为准（部分文件 → 部分更新）；
// 代码内直接构造（Present 为 nil）视为全字段提供（全量更新），保持与导出文件往返语义一致。
func (item *EleAgentModelExportItem) Has(field string) bool {
	if item.Present == nil {
		return true
	}
	return item.Present[field]
}

// EleAgentModelExportData 导出文件整体结构；导入接口同样接受该结构或纯数组。
// Usage 与 FieldNotes 仅供人工阅读（JSON 不支持注释），导入时被忽略。
type EleAgentModelExportData struct {
	Version     int                       `json:"version"`
	ExportedAt  string                    `json:"exported_at"`
	IncludeKeys bool                      `json:"include_keys"`
	Usage       string                    `json:"usage"`
	FieldNotes  map[string]string         `json:"field_notes"`
	Items       []EleAgentModelExportItem `json:"items"`
}

// EleAgentModelExportUsage 导出文件顶部的导入规则说明
const EleAgentModelExportUsage = "本文件由 Ele Agent 模型配置导出，可直接用于批量导入：按 provider + model_name 匹配，已存在则只覆盖文件中出现的字段（api_key 省略=保留原 Key，提供=轮换），不存在则创建（需完整字段与 api_key）。usage 与 field_notes 仅供阅读，导入时忽略。"

// eleAgentModelFieldNotes 逐字段含义与取值范围说明，随导出文件带出，便于人工编辑
var eleAgentModelFieldNotes = map[string]string{
	"provider":                    "平台标识（自定义，用于配置匹配与统计），如 kimi / volcengine / agnes / qwen；与 model_name 组成唯一匹配键",
	"protocol":                    "上游协议：openai_compatible（对话）/ anthropic_messages（对话）/ agnes_image（图片）/ agnes_video（视频）/ seedance（火山视频）/ seedream（火山方舟·即梦图片）/ openai_image、openai_video（预留）；缺省为 openai_compatible",
	"model_name":                  "上游模型 ID，如 k3、doubao-seedream-4-0-250828、doubao-seedance-1-0-pro-250528",
	"display_name":                "展示名称（可选），客户端模型列表中显示",
	"base_url":                    "上游 API 地址；新建必填，更新时省略表示保持原值",
	"api_key":                     "明文 API Key；新建必填；更新时省略=保留原 Key，提供=轮换 Key",
	"is_enabled":                  "是否启用；更新时省略表示保持原启用状态",
	"supports_chat":               "能力开关：支持文字对话（对话页）；纯图片/纯视频生成模型应为 false，对话/图片/视频至少需开启一项",
	"supports_vision":             "能力开关：支持视觉理解（图片输入）",
	"supports_image":              "能力开关：支持图片生成（需搭配 agnes_image / seedream 协议）",
	"supports_video":              "能力开关：支持视频生成（需搭配 agnes_video / seedance 协议）",
	"supports_image_input":        "能力开关：支持上传图片作为生成输入（图生图/图生视频）",
	"supports_continuous_context": "能力开关（产品声明）：支持连续上下文创作，运行时由 protocol 决定",
	"supports_tools":              "能力开关：支持 Agent 工具调用（Function Call）",
	"priority":                    "优先级（整数 ≥0，越小越靠前），用于客户端模型列表排序",
	"input_price_per_call":        "输入单价（弹丸 / 1M tokens，≥0），0 表示免费",
	"price_per_call":              "输出单价（弹丸 / 1M tokens，≥0），0 表示免费",
	"price_per_generation":        "按次附加费（弹丸/次，≥0），与输入/输出 token 费用相加，适用于对话/图片/视频模型，0 表示不附加",
	"video_min_duration":          "视频最小时长（秒，≥0），0 表示不限制；不能超过 video_max_duration",
	"video_max_duration":          "视频最大时长（秒，≥0），0 表示不限制；示例：Seedance 1.0 Pro 支持 5~10 秒",
	"video_duration_step":         "视频时长步长（秒，≥1），前端按 min~max 以步长生成可选档位；示例：5~10 秒步长 5 → 可选 5s / 10s",
}

// EleAgentModelImportFailure 单行导入失败信息
type EleAgentModelImportFailure struct {
	Index     int    `json:"index"`
	Provider  string `json:"provider"`
	ModelName string `json:"model_name"`
	Error     string `json:"error"`
}

// EleAgentModelImportResult 批量导入结果汇总
type EleAgentModelImportResult struct {
	Created int                          `json:"created"`
	Updated int                          `json:"updated"`
	Failed  []EleAgentModelImportFailure `json:"failed"`
}

// ExportConfigs 导出全部模型配置；includeKeys 为 true 时解密带出明文 API Key。
func (s *EleAgentModelService) ExportConfigs(includeKeys bool) (*EleAgentModelExportData, error) {
	if s.repo == nil {
		return nil, errors.New("EleAgentModelService 未初始化存储")
	}
	items, _, err := s.repo.List("", 1, 10000)
	if err != nil {
		return nil, err
	}

	out := &EleAgentModelExportData{
		Version:     1,
		ExportedAt:  time.Now().Format(time.RFC3339),
		IncludeKeys: includeKeys,
		Usage:       EleAgentModelExportUsage,
		FieldNotes:  eleAgentModelFieldNotes,
		Items:       make([]EleAgentModelExportItem, 0, len(items)),
	}
	for _, cfg := range items {
		enabled := cfg.IsEnabled
		item := EleAgentModelExportItem{
			Provider:                  cfg.Provider,
			Protocol:                  string(cfg.Protocol),
			ModelName:                 cfg.ModelName,
			DisplayName:               cfg.DisplayName,
			BaseURL:                   cfg.BaseURL,
			IsEnabled:                 &enabled,
			SupportsChat:              cfg.SupportsChat,
			SupportsVision:            cfg.SupportsVision,
			SupportsImage:             cfg.SupportsImage,
			SupportsVideo:             cfg.SupportsVideo,
			SupportsImageInput:        cfg.SupportsImageInput,
			SupportsContinuousContext: cfg.SupportsContinuousContext,
			SupportsTools:             cfg.SupportsTools,
			Priority:                  cfg.Priority,
			InputPricePerCall:         cfg.InputPricePerCall,
			PricePerCall:              cfg.PricePerCall,
			PricePerGeneration:        cfg.PricePerGeneration,
			VideoMinDuration:          cfg.VideoMinDuration,
			VideoMaxDuration:          cfg.VideoMaxDuration,
			VideoDurationStep:         cfg.VideoDurationStep,
		}
		if includeKeys {
			key, err := s.decryptKey(cfg)
			if err != nil {
				return nil, fmt.Errorf("解密 %s/%s 的 API Key 失败: %w", cfg.Provider, cfg.ModelName, err)
			}
			item.APIKey = key
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// ImportConfigs 批量导入模型配置。
// 按 provider + model_name 匹配：
//   - 已存在：只覆盖文件中出现的字段（未出现的字段保持原值；api_key 不提供时保留原 Key）；
//     协议-能力一致性、时长范围等交叉校验按「出现取文件值、未出现取现有值」的有效值进行，
//     避免部分更新把配置改成不一致状态；
//   - 不存在：创建（必须提供完整字段与 api_key）。
//
// 逐行处理，单行失败不影响其他行。
func (s *EleAgentModelService) ImportConfigs(items []EleAgentModelExportItem) (*EleAgentModelImportResult, error) {
	if s.repo == nil {
		return nil, errors.New("EleAgentModelService 未初始化存储")
	}
	if s.encrypt == nil {
		return nil, errors.New("未配置 ENCRYPTION_MASTER_KEY，无法处理 API Key")
	}

	// 预取全部配置（含禁用），建立 provider/model -> 现有配置 映射
	existing, _, err := s.repo.List("", 1, 10000)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*model.EleAgentModelConfig, len(existing))
	for _, cfg := range existing {
		byKey[cfg.Provider+"/"+cfg.ModelName] = cfg
	}

	result := &EleAgentModelImportResult{Failed: make([]EleAgentModelImportFailure, 0)}
	seen := make(map[string]bool, len(items))

	for i, item := range items {
		fail := func(msg string) {
			result.Failed = append(result.Failed, EleAgentModelImportFailure{
				Index:     i,
				Provider:  item.Provider,
				ModelName: item.ModelName,
				Error:     msg,
			})
		}

		// 基础字段：provider/model_name 为匹配键必填；base_url 仅新建必填，更新时出现则不允许为空
		item.Provider = strings.TrimSpace(item.Provider)
		item.ModelName = strings.TrimSpace(item.ModelName)
		item.BaseURL = strings.TrimSpace(item.BaseURL)
		if item.Provider == "" || item.ModelName == "" {
			fail("provider、model_name 不能为空")
			continue
		}
		if item.Has("base_url") && item.BaseURL == "" {
			fail("base_url 不能为空")
			continue
		}

		mapKey := item.Provider + "/" + item.ModelName
		if seen[mapKey] {
			fail("导入文件内存在重复的 provider/model_name")
			continue
		}
		seen[mapKey] = true
		cfg, exists := byKey[mapKey]

		if !exists {
			// ==================== 新建路径：完整字段校验 ====================
			if item.BaseURL == "" {
				fail("新建配置必须提供 base_url")
				continue
			}
			protocol := item.Protocol
			if protocol == "" {
				protocol = string(model.EleAgentUpstreamOpenAICompatible)
			}
			if err := validateProtocolCapabilities(protocol, item.SupportsChat, item.SupportsImage, item.SupportsVideo); err != nil {
				fail(err.Error())
				continue
			}
			if item.Priority < 0 || item.InputPricePerCall < 0 || item.PricePerCall < 0 || item.PricePerGeneration < 0 ||
				item.VideoMinDuration < 0 || item.VideoMaxDuration < 0 || item.VideoDurationStep < 0 {
				fail("单价、时长与优先级不能为负数")
				continue
			}
			if item.VideoMaxDuration > 0 && item.VideoMinDuration > item.VideoMaxDuration {
				fail("视频最小时长不能大于最大时长")
				continue
			}
			if item.APIKey == "" {
				fail("新建配置必须提供 api_key")
				continue
			}
			isEnabled := true
			if item.IsEnabled != nil {
				isEnabled = *item.IsEnabled
			}
			ciphertext, nonce, version, err := s.encrypt.Encrypt(item.APIKey)
			if err != nil {
				fail("加密 API Key 失败: " + err.Error())
				continue
			}
			newCfg := &model.EleAgentModelConfig{
				ID:                        uuid.New().String(),
				Provider:                  item.Provider,
				Protocol:                  model.EleAgentUpstreamProtocol(protocol),
				ModelName:                 item.ModelName,
				DisplayName:               item.DisplayName,
				BaseURL:                   item.BaseURL,
				EncryptedKey:              ciphertext,
				Nonce:                     nonce,
				KeyVersion:                version,
				IsEnabled:                 isEnabled,
				SupportsChat:              item.SupportsChat,
				SupportsVision:            item.SupportsVision,
				SupportsImage:             item.SupportsImage,
				SupportsVideo:             item.SupportsVideo,
				SupportsImageInput:        item.SupportsImageInput,
				SupportsContinuousContext: item.SupportsContinuousContext,
				SupportsTools:             item.SupportsTools,
				Priority:                  item.Priority,
				InputPricePerCall:         item.InputPricePerCall,
				PricePerCall:              item.PricePerCall,
				PricePerGeneration:        item.PricePerGeneration,
				VideoMinDuration:          item.VideoMinDuration,
				VideoMaxDuration:          item.VideoMaxDuration,
				VideoDurationStep:         item.VideoDurationStep,
			}
			if err := s.repo.Create(newCfg); err != nil {
				fail("创建失败: " + err.Error())
				continue
			}
			byKey[mapKey] = newCfg
			result.Created++
			continue
		}

		// ==================== 更新路径：只覆盖文件中出现的字段 ====================
		// 有效值（出现取文件值、未出现取现有值）做交叉校验，避免部分更新把配置改成不一致状态
		effProtocol := string(cfg.Protocol)
		if item.Has("protocol") {
			effProtocol = item.Protocol
			if effProtocol == "" {
				effProtocol = string(model.EleAgentUpstreamOpenAICompatible)
			}
		}
		effSupportsChat := cfg.SupportsChat
		if item.Has("supports_chat") {
			effSupportsChat = item.SupportsChat
		}
		effSupportsImage := cfg.SupportsImage
		if item.Has("supports_image") {
			effSupportsImage = item.SupportsImage
		}
		effSupportsVideo := cfg.SupportsVideo
		if item.Has("supports_video") {
			effSupportsVideo = item.SupportsVideo
		}
		if err := validateProtocolCapabilities(effProtocol, effSupportsChat, effSupportsImage, effSupportsVideo); err != nil {
			fail(err.Error())
			continue
		}
		if (item.Has("priority") && item.Priority < 0) ||
			(item.Has("input_price_per_call") && item.InputPricePerCall < 0) ||
			(item.Has("price_per_call") && item.PricePerCall < 0) ||
			(item.Has("price_per_generation") && item.PricePerGeneration < 0) ||
			(item.Has("video_min_duration") && item.VideoMinDuration < 0) ||
			(item.Has("video_max_duration") && item.VideoMaxDuration < 0) ||
			(item.Has("video_duration_step") && item.VideoDurationStep < 0) {
			fail("单价、时长与优先级不能为负数")
			continue
		}
		effMinDuration := cfg.VideoMinDuration
		if item.Has("video_min_duration") {
			effMinDuration = item.VideoMinDuration
		}
		effMaxDuration := cfg.VideoMaxDuration
		if item.Has("video_max_duration") {
			effMaxDuration = item.VideoMaxDuration
		}
		if effMaxDuration > 0 && effMinDuration > effMaxDuration {
			fail("视频最小时长不能大于最大时长")
			continue
		}

		updates := map[string]interface{}{}
		if item.Has("protocol") {
			updates["protocol"] = model.EleAgentUpstreamProtocol(effProtocol)
		}
		if item.Has("display_name") {
			updates["display_name"] = item.DisplayName
		}
		if item.Has("base_url") {
			updates["base_url"] = item.BaseURL
		}
		if item.Has("supports_chat") {
			updates["supports_chat"] = item.SupportsChat
		}
		if item.Has("supports_vision") {
			updates["supports_vision"] = item.SupportsVision
		}
		if item.Has("supports_image") {
			updates["supports_image"] = item.SupportsImage
		}
		if item.Has("supports_video") {
			updates["supports_video"] = item.SupportsVideo
		}
		if item.Has("supports_image_input") {
			updates["supports_image_input"] = item.SupportsImageInput
		}
		if item.Has("supports_continuous_context") {
			updates["supports_continuous_context"] = item.SupportsContinuousContext
		}
		if item.Has("supports_tools") {
			updates["supports_tools"] = item.SupportsTools
		}
		if item.Has("priority") {
			updates["priority"] = item.Priority
		}
		if item.Has("input_price_per_call") {
			updates["input_price_per_call"] = item.InputPricePerCall
		}
		if item.Has("price_per_call") {
			updates["price_per_call"] = item.PricePerCall
		}
		if item.Has("price_per_generation") {
			updates["price_per_generation"] = item.PricePerGeneration
		}
		if item.Has("video_min_duration") {
			updates["video_min_duration"] = item.VideoMinDuration
		}
		if item.Has("video_max_duration") {
			updates["video_max_duration"] = item.VideoMaxDuration
		}
		if item.Has("video_duration_step") {
			updates["video_duration_step"] = item.VideoDurationStep
		}
		if item.IsEnabled != nil {
			updates["is_enabled"] = *item.IsEnabled
		}
		if item.APIKey != "" {
			ciphertext, nonce, version, err := s.encrypt.Encrypt(item.APIKey)
			if err != nil {
				fail("加密 API Key 失败: " + err.Error())
				continue
			}
			updates["encrypted_key"] = ciphertext
			updates["nonce"] = nonce
			updates["key_version"] = version
		}
		if len(updates) > 0 {
			if err := s.repo.UpdateFields(cfg.ID, updates); err != nil {
				fail("更新失败: " + err.Error())
				continue
			}
		}
		result.Updated++
	}

	_ = s.reload()
	return result, nil
}

// validateProtocolCapabilities 校验模型的协议与能力标识是否一致。规则：
//  1. 对话/图片/视频至少支持一项，否则模型没有任何可用入口；
//  2. 视觉生成协议（agnes_image/seedream 为图片，agnes_video/seedance 为视频）与媒体能力必须匹配；
//  3. 对话协议（openai_compatible/anthropic_messages 等）不允许声明图片/视频生成能力；
//  4. 视觉协议不限制 supports_chat，预留多模态模型既对话又生成的可能。
//
// 避免管理后台把视频模型配成 agnes_image 等错误组合，导致用户生成时提示不支持 media_type；
// 也避免纯视觉模型在对话页被选中发起文字对话。
func validateProtocolCapabilities(protocol string, supportsChat, supportsImage, supportsVideo bool) error {
	if !supportsChat && !supportsImage && !supportsVideo {
		return errors.New("模型至少需要支持对话生成、图片生成、视频生成中的一项能力")
	}
	if !supportsImage && !supportsVideo {
		return nil
	}
	switch protocol {
	case string(model.EleAgentUpstreamAgnesImage), string(model.EleAgentUpstreamSeedream):
		if supportsVideo {
			return errors.New("图片生成协议（Agnes Image / Seedream）不支持视频生成，如需视频请选择 Agnes Video 或 Seedance 协议")
		}
	case string(model.EleAgentUpstreamAgnesVideo), string(model.EleAgentUpstreamSeedance):
		if supportsImage {
			return errors.New("视频生成协议不支持图片生成，如需图片请选择 Agnes Image 或 Seedream 协议")
		}
	default:
		if supportsImage || supportsVideo {
			return fmt.Errorf("视觉生成模型协议必须是 agnes_image、agnes_video、seedance 或 seedream，当前为 %s", protocol)
		}
	}
	return nil
}

// toEleAgentModelListItem 转换为列表项（脱敏）
func toEleAgentModelListItem(config *model.EleAgentModelConfig) *model.EleAgentModelListItem {
	return &model.EleAgentModelListItem{
		ID:                        config.ID,
		Provider:                  config.Provider,
		Protocol:                  config.Protocol,
		ModelName:                 config.ModelName,
		DisplayName:               config.DisplayName,
		BaseURL:                   config.BaseURL,
		KeyVersion:                config.KeyVersion,
		IsEnabled:                 config.IsEnabled,
		SupportsChat:              config.SupportsChat,
		SupportsVision:            config.SupportsVision,
		SupportsImage:             config.SupportsImage,
		SupportsVideo:             config.SupportsVideo,
		SupportsImageInput:        config.SupportsImageInput,
		SupportsContinuousContext: config.SupportsContinuousContext,
		SupportsTools:             config.SupportsTools,
		Priority:                  config.Priority,
		InputPricePerCall:         config.InputPricePerCall,
		PricePerCall:              config.PricePerCall,
		PricePerGeneration:        config.PricePerGeneration,
		VideoMinDuration:          config.VideoMinDuration,
		VideoMaxDuration:          config.VideoMaxDuration,
		VideoDurationStep:         config.VideoDurationStep,
		CreatedAt:                 config.CreatedAt,
		UpdatedAt:                 config.UpdatedAt,
	}
}

// EnsureDefaultConfigs 若没有配置则写入默认 Qwen3-8B 配置
// 用于开发/测试环境快速启动，生产环境应通过管理后台配置。
func (s *EleAgentModelService) EnsureDefaultConfigs() error {
	if s.repo == nil {
		return nil
	}

	_, total, err := s.repo.List("", 1, 1)
	if err != nil {
		return err
	}
	if total > 0 {
		return nil
	}

	// 开发/测试兜底：Qwen3-8B 通过环境变量读取 API Key
	apiKey := os.Getenv("QWEN_API_KEY")
	if apiKey == "" {
		return nil
	}

	_, err = s.CreateConfig(EleAgentModelConfigInput{
		Provider:      "qwen",
		Protocol:      string(model.EleAgentUpstreamOpenAICompatible),
		ModelName:     "Qwen/Qwen3-8B",
		DisplayName:   "通义千问 Qwen3-8B",
		BaseURL:       "https://api.siliconflow.cn/v1",
		APIKey:        apiKey,
		SupportsChat:  true,
		SupportsTools: true,
	})
	return err
}
