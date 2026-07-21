package model

import "time"

// VisualMediaType 视觉生成媒体类型
type VisualMediaType string

const (
	// VisualMediaTypeImage 图片生成
	VisualMediaTypeImage VisualMediaType = "image"
	// VisualMediaTypeVideo 视频生成
	VisualMediaTypeVideo VisualMediaType = "video"
)

// VisualTaskStatus 视觉生成任务状态
type VisualTaskStatus string

const (
	// VisualTaskStatusPending 等待中（视频任务创建后初始状态）
	VisualTaskStatusPending VisualTaskStatus = "pending"
	// VisualTaskStatusRunning 生成中
	VisualTaskStatusRunning VisualTaskStatus = "running"
	// VisualTaskStatusSucceeded 生成成功
	VisualTaskStatusSucceeded VisualTaskStatus = "succeeded"
	// VisualTaskStatusFailed 生成失败
	VisualTaskStatusFailed VisualTaskStatus = "failed"
	// VisualTaskStatusCancelled 已取消
	VisualTaskStatusCancelled VisualTaskStatus = "cancelled"
)

// VisualProvider 视觉生成 Provider 标识
type VisualProvider string

const (
	// VisualProviderAgnesImage Agnes Image
	VisualProviderAgnesImage VisualProvider = "agnes_image"
	// VisualProviderAgnesVideo Agnes Video
	VisualProviderAgnesVideo VisualProvider = "agnes_video"
	// VisualProviderSeedance 火山引擎 Seedance
	VisualProviderSeedance VisualProvider = "seedance"
)

// VisualGenerationTask 统一的视觉生成任务表
// 同时承载图片同步生成与视频异步生成，按 media_type 区分。
type VisualGenerationTask struct {
	ID             string         `gorm:"primaryKey;type:varchar(32)" json:"id"`
	UserID         string         `gorm:"index:idx_visual_tasks_user_media_status;not null;type:varchar(32)" json:"user_id"`
	ConversationID string         `gorm:"index;type:varchar(64)" json:"conversation_id"`
	MediaType      VisualMediaType `gorm:"index:idx_visual_tasks_user_media_status;not null;type:varchar(16)" json:"media_type"`
	Provider       VisualProvider `gorm:"not null;type:varchar(32)" json:"provider"`
	Model          string         `gorm:"not null;type:varchar(128)" json:"model"`
	UpstreamTaskID string         `gorm:"index;type:varchar(128)" json:"upstream_task_id"`
	Status         VisualTaskStatus `gorm:"index:idx_visual_tasks_user_media_status;not null;type:varchar(32)" json:"status"`
	Prompt         string         `gorm:"type:text" json:"prompt"`
	Params         string         `gorm:"type:text" json:"params"`           // JSON 字符串
	InputAssets    string         `gorm:"type:text" json:"input_assets"`     // JSON 字符串
	Result         string         `gorm:"type:text" json:"result"`           // JSON 字符串
	LocalAssetIDs  string         `gorm:"type:text" json:"local_asset_ids"`  // JSON 数组：本地转存文件 ID 列表
	ErrorMessage   string         `gorm:"type:text" json:"error_message"`
	Usage          string         `gorm:"type:text" json:"usage"`            // JSON 字符串
	Cost           int64          `json:"cost"`
	Currency       string         `gorm:"default:'danwan';type:varchar(16)" json:"currency"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	CompletedAt    *time.Time     `json:"completed_at"`
}

// TableName 指定表名
func (VisualGenerationTask) TableName() string {
	return "visual_generation_tasks"
}
