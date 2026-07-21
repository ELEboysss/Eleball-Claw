package handler

import (
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eleball/gateway/internal/model"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

// VisualHandler 视觉生成处理器
type VisualHandler struct {
	visualService *service.VisualGenerationService
	uploadService *service.VisualUploadService
	convService   *service.VisualConversationService
}

// NewVisualHandler 创建处理器
func NewVisualHandler(visualService *service.VisualGenerationService, uploadService *service.VisualUploadService, convService *service.VisualConversationService) *VisualHandler {
	return &VisualHandler{visualService: visualService, uploadService: uploadService, convService: convService}
}

// formatVisualValidationErrors 把 Gin validator 的错误转换成用户可读的文案
func formatVisualValidationErrors(err error) string {
	var ve validator.ValidationErrors
	if !stderrors.As(err, &ve) {
		return "参数错误: " + err.Error()
	}

	msgs := make([]string, 0, len(ve))
	for _, e := range ve {
		switch e.Field() {
		case "Provider":
			msgs = append(msgs, "请选择模型提供方")
		case "Model":
			msgs = append(msgs, "请选择具体的模型名称")
		case "Prompt":
			msgs = append(msgs, "请输入描述提示词")
		case "MediaType":
			msgs = append(msgs, "请选择生成类型")
		default:
			msgs = append(msgs, fmt.Sprintf("%s 为必填项", e.Field()))
		}
	}
	return "请完善以下信息：" + strings.Join(msgs, "、")
}

// CreateTask 创建视觉生成任务
func (h *VisualHandler) CreateTask(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var req service.CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": formatVisualValidationErrors(err)})
		return
	}

	task, err := h.visualService.CreateTask(c.Request.Context(), userID.(string), &req)
	if err != nil {
		if stderrors.Is(err, service.UpstreamRateLimitedError) {
			c.JSON(http.StatusTooManyRequests, gin.H{"code": 1000, "message": "服务繁忙，请稍后再试"})
			return
		}
		if _, ok := err.(*service.BalanceInsufficientError); ok {
			c.JSON(http.StatusPaymentRequired, gin.H{"code": 4002, "message": err.Error()})
			return
		}
		if _, ok := err.(*service.PromptFusionModelNotConfiguredError); ok {
			c.JSON(http.StatusBadRequest, gin.H{"code": 4003, "message": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    task,
	})
}

// UploadFile 上传参考图/首帧图
func (h *VisualHandler) UploadFile(c *gin.Context) {
	userID, _ := c.Get("user_id")

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "读取上传文件失败: " + err.Error()})
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "读取文件内容失败: " + err.Error()})
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	result, err := h.uploadService.Save(userID.(string), data, contentType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": err.Error()})
		return
	}

	// 返回绝对公网 URL，便于上游视觉厂商直接拉取
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if fwdProto := c.GetHeader("X-Forwarded-Proto"); fwdProto != "" {
		scheme = fwdProto
	}
	baseURL := scheme + "://" + c.Request.Host

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"id":       result.ID,
			"url":      baseURL + result.URL,
			"mime_type": result.MIMEType,
		},
	})
}

// GetFile 提供上传图片的公网访问（用于上游拉取参考图/首帧图）
func (h *VisualHandler) GetFile(c *gin.Context) {
	id := filepath.Base(c.Param("id"))
	path, mimeType, err := h.uploadService.GetPath(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": err.Error()})
		return
	}

	data, err := h.uploadService.ReadFile(path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": "读取文件失败: " + err.Error()})
		return
	}

	c.Data(http.StatusOK, mimeType, data)
}

// GetTask 查询任务状态
func (h *VisualHandler) GetTask(c *gin.Context) {
	userID, _ := c.Get("user_id")
	taskID := c.Param("id")

	task, err := h.visualService.QueryTask(c.Request.Context(), taskID, userID.(string))
	if err != nil {
		if stderrors.Is(err, service.UpstreamRateLimitedError) {
			c.JSON(http.StatusTooManyRequests, gin.H{"code": 1000, "message": "服务繁忙，请稍后再试"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    task,
	})
}

// CancelTask 取消任务
func (h *VisualHandler) CancelTask(c *gin.Context) {
	userID, _ := c.Get("user_id")
	taskID := c.Param("id")

	task, err := h.visualService.CancelTask(c.Request.Context(), taskID, userID.(string))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    task,
	})
}


// CreateConversation 创建视觉创作会话
func (h *VisualHandler) CreateConversation(c *gin.Context) {
	userID, _ := c.Get("user_id")

	var req struct {
		Title     string `json:"title" binding:"required"`
		MediaType string `json:"media_type"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	conv, err := h.convService.CreateConversation(userID.(string), req.Title, req.MediaType)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": conv})
}

// ListConversations 列出当前用户的视觉创作会话
func (h *VisualHandler) ListConversations(c *gin.Context) {
	userID, _ := c.Get("user_id")
	page, _ := strconv.Atoi(c.Query("page"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	mediaType := c.Query("media_type")
	items, total, err := h.convService.ListConversations(userID.(string), mediaType, page, pageSize)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		},
	})
}

// GetConversation 获取会话详情及任务列表
// 对未完成的视频任务会主动查询上游刷新状态，避免前端轮询时永远看到 pending/running。
func (h *VisualHandler) GetConversation(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := userID.(string)
	id := c.Param("id")

	conv, tasks, err := h.convService.GetConversation(id, uid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 1000, "message": "会话不存在或无权访问"})
		return
	}

	// 刷新未完成视频任务状态，保证前端拿到的是最新进展
	for i := range tasks {
		t := &tasks[i]
		if t.MediaType == model.VisualMediaTypeVideo && (t.Status == model.VisualTaskStatusPending || t.Status == model.VisualTaskStatusRunning) {
			fresh, err := h.visualService.QueryTask(c.Request.Context(), t.ID, uid)
			if err == nil {
				tasks[i] = *fresh
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data": gin.H{
			"conversation": conv,
			"tasks":        tasks,
		},
	})
}

// UpdateConversation 更新视觉会话标题
func (h *VisualHandler) UpdateConversation(c *gin.Context) {
	userID, _ := c.Get("user_id")
	id := c.Param("id")

	var req struct {
		Title string `json:"title" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	if err := h.convService.UpdateConversationTitle(id, userID.(string), req.Title); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// DeleteConversation 删除视觉创作会话
func (h *VisualHandler) DeleteConversation(c *gin.Context) {
	userID, _ := c.Get("user_id")
	id := c.Param("id")

	if err := h.convService.DeleteConversation(id, userID.(string)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 4001, "message": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}
