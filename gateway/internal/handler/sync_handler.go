package handler

import (
	"net/http"

	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SyncHandler 多设备同步处理器
type SyncHandler struct {
	repo *repository.ConversationRepo
}

// NewSyncHandler 创建处理器
func NewSyncHandler(repo *repository.ConversationRepo) *SyncHandler {
	return &SyncHandler{repo: repo}
}

// PushRequest 推送请求
type PushRequest struct {
	Records []SyncRecordDTO `json:"records" binding:"required"`
}

// SyncRecordDTO 同步记录 DTO
type SyncRecordDTO struct {
	EntityType        string `json:"entity_type" binding:"required"`
	EntityID          string `json:"entity_id" binding:"required"`
	Operation         string `json:"operation" binding:"required"`
	SyncVersion       int64  `json:"sync_version" binding:"required"`
	PayloadCiphertext string `json:"payload_ciphertext" binding:"required"`
}

// Push 设备上传增量数据
func (h *SyncHandler) Push(c *gin.Context) {
	var req PushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	userID, _ := c.Get("user_id")
	deviceID, _ := c.Get("device_id")

	for _, r := range req.Records {
		record := &model.Conversation{
			ID:                uuid.New().String(),
			UserID:            userID.(string),
			DeviceID:          deviceID.(string),
			EntityType:        r.EntityType,
			EntityID:          r.EntityID,
			Operation:         r.Operation,
			SyncVersion:       r.SyncVersion,
			PayloadCiphertext: r.PayloadCiphertext,
		}
		if err := h.repo.SaveSyncRecord(record); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// Pull 设备拉取增量数据
func (h *SyncHandler) Pull(c *gin.Context) {
	var req struct {
		MinVersion int64 `json:"min_version"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误"})
		return
	}

	userID, _ := c.Get("user_id")

	records, err := h.repo.PullSyncRecords(userID.(string), req.MinVersion)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	maxVersion, _ := h.repo.GetMaxSyncVersion(userID.(string))

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"records":     records,
			"max_version": maxVersion,
		},
	})
}
