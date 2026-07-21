package service

import (
	"strconv"

	"github.com/eleball/gateway/internal/repository"
)

// SettingService 系统设置服务
type SettingService struct {
	repo *repository.SettingRepo
}

// NewSettingService 创建设置服务
func NewSettingService(repo *repository.SettingRepo) *SettingService {
	return &SettingService{repo: repo}
}

// Settings 当前支持的系统设置字段
type Settings struct {
	SiteName            string `json:"site_name"`
	RegisterOpen        bool   `json:"register_open"`
	DefaultModel        string `json:"default_model"`
	MaxTokensPerRequest int    `json:"max_tokens_per_request"`
	FreeQuota           int    `json:"free_quota"`
	MaintenanceMode     bool   `json:"maintenance_mode"`
	XianyuProductURL    string `json:"xianyu_product_url"`
	TaobaoProductURL    string `json:"taobao_product_url"`
	// PromptFusionModel 视觉连续创作时用于 prompt 融合的 EleAgent 对话模型，格式 provider/model_name
	PromptFusionModel string `json:"prompt_fusion_model"`
}

const (
	KeySiteName            = "site_name"
	KeyRegisterOpen        = "register_open"
	KeyDefaultModel        = "default_model"
	KeyMaxTokensPerRequest = "max_tokens_per_request"
	KeyFreeQuota           = "free_quota"
	KeyMaintenanceMode     = "maintenance_mode"
	KeyXianyuProductURL    = "xianyu_product_url"
	KeyTaobaoProductURL    = "taobao_product_url"
	KeyPromptFusionModel   = "prompt_fusion_model"
)

// defaults 返回默认配置
func (s *SettingService) defaults() *Settings {
	return &Settings{
		SiteName:            "Eleball",
		RegisterOpen:        true,
		DefaultModel:        "qwen/Qwen/Qwen3-8B",
		MaxTokensPerRequest: 4096,
		FreeQuota:           1000,
		MaintenanceMode:     false,
		XianyuProductURL:    "",
		TaobaoProductURL:    "",
		PromptFusionModel:   "",
	}
}

// GetSettings 获取系统设置，缺失字段使用默认值补齐
func (s *SettingService) GetSettings() (*Settings, error) {
	all, err := s.repo.GetAll()
	if err != nil {
		return nil, err
	}

	settings := s.defaults()
	if v, ok := all[KeySiteName]; ok {
		settings.SiteName = v
	}
	if v, ok := all[KeyRegisterOpen]; ok {
		settings.RegisterOpen, _ = strconv.ParseBool(v)
	}
	if v, ok := all[KeyDefaultModel]; ok {
		settings.DefaultModel = v
	}
	if v, ok := all[KeyMaxTokensPerRequest]; ok {
		settings.MaxTokensPerRequest, _ = strconv.Atoi(v)
	}
	if v, ok := all[KeyFreeQuota]; ok {
		settings.FreeQuota, _ = strconv.Atoi(v)
	}
	if v, ok := all[KeyMaintenanceMode]; ok {
		settings.MaintenanceMode, _ = strconv.ParseBool(v)
	}
	if v, ok := all[KeyXianyuProductURL]; ok {
		settings.XianyuProductURL = v
	}
	if v, ok := all[KeyTaobaoProductURL]; ok {
		settings.TaobaoProductURL = v
	}
	if v, ok := all[KeyPromptFusionModel]; ok {
		settings.PromptFusionModel = v
	}
	return settings, nil
}

// UpdateSettings 更新系统设置
func (s *SettingService) UpdateSettings(req *Settings) error {
	updates := map[string]string{
		KeySiteName:            req.SiteName,
		KeyDefaultModel:        req.DefaultModel,
		KeyMaxTokensPerRequest: strconv.Itoa(req.MaxTokensPerRequest),
		KeyFreeQuota:           strconv.Itoa(req.FreeQuota),
		KeyRegisterOpen:        strconv.FormatBool(req.RegisterOpen),
		KeyMaintenanceMode:     strconv.FormatBool(req.MaintenanceMode),
		KeyXianyuProductURL:    req.XianyuProductURL,
		KeyTaobaoProductURL:    req.TaobaoProductURL,
		KeyPromptFusionModel:   req.PromptFusionModel,
	}
	return s.repo.MSet(updates)
}
