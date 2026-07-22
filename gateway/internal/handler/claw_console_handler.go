package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ClawConsoleHandler claw 本地控制台处理器（P3 细化）。
//
// 提供本地 token 用量统计等本地控制台端点（替代云端 admin 的 DAU/收入统计）。
// claw 本地不计费，但记录 token 用量用于本地观察；不展示平台级数据。
type ClawConsoleHandler struct {
	db *gorm.DB
}

// NewClawConsoleHandler 创建 claw 控制台处理器
func NewClawConsoleHandler(db *gorm.DB) *ClawConsoleHandler {
	return &ClawConsoleHandler{db: db}
}

// tokenUsageStats 本地 token 用量聚合
type tokenUsageStats struct {
	TotalCalls       int64 `json:"total_calls"`
	TodayCalls       int64 `json:"today_calls"`
	TotalInputTokens int64 `json:"total_input_tokens"`
	TotalOutputTokens int64 `json:"total_output_tokens"`
}

// modelUsage 单模型用量
type modelUsage struct {
	ModelID    string `json:"model_id"`
	Provider   string `json:"provider"`
	Calls      int64  `json:"calls"`
	InputTokens int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// GetStats 本地 token 用量统计（P3 细化，plan §D.2）。
// GET /v1/claw-console/stats
func (h *ClawConsoleHandler) GetStats(c *gin.Context) {
	var stats tokenUsageStats
	h.db.Table("token_usages").
		Select("COUNT(*) as total_calls, COALESCE(SUM(input_tokens),0) as total_input_tokens, COALESCE(SUM(output_tokens),0) as total_output_tokens").
		Scan(&stats)
	// 今日调用（本地时区当天 00:00 起）
	h.db.Table("token_usages").
		Where("created_at >= date('now','localtime')").
		Count(&stats.TodayCalls)

	var models []modelUsage
	h.db.Table("token_usages").
		Select("model_id, provider, COUNT(*) as calls, COALESCE(SUM(input_tokens),0) as input_tokens, COALESCE(SUM(output_tokens),0) as output_tokens").
		Group("model_id, provider").
		Order("calls DESC").
		Limit(10).
		Scan(&models)

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"usage":  stats,
			"models": models,
		},
	})
}
