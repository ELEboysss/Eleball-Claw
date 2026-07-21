package handler

import (
	"net/http"
	"time"

	"github.com/eleball/gateway/internal/repository"
	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// SttHandler 语音识别代理处理器
type SttHandler struct {
	sttService     *service.SttService
	userRepo       *repository.UserRepo
	vipService     *service.VIPService
	defaultMonthly int64
	logger         *zap.Logger
}

// NewSttHandler 创建语音识别处理器
func NewSttHandler(sttService *service.SttService, userRepo *repository.UserRepo, vipService *service.VIPService, logger *zap.Logger) *SttHandler {
	return &SttHandler{
		sttService:     sttService,
		userRepo:       userRepo,
		vipService:     vipService,
		defaultMonthly: 1000, // 兜底默认值
		logger:         logger,
	}
}

// Transcribe 语音转文本接口
// 客户端上传音频文件（multipart/form-data，字段名 file），网关代理到国内 ASR 服务。
func (h *SttHandler) Transcribe(c *gin.Context) {
	if h.sttService == nil || !h.sttService.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 3002, "message": "语音识别服务未配置"})
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "请上传音频文件: " + err.Error()})
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "打开音频文件失败: " + err.Error()})
		return
	}
	defer file.Close()

	// 检查并扣减用户 ASR 月度额度（按 VIP 等级）
	userID, _ := c.Get("user_id")
	if uid, ok := userID.(string); ok && uid != "" {
		quota := h.defaultMonthly
		if h.vipService != nil {
			if vipQuota, err := h.vipService.GetAsrQuotaMonthly(uid); err == nil && vipQuota > 0 {
				quota = vipQuota
			}
		}
		_, err = h.userRepo.CheckAndUseAsrQuota(uid, quota, time.Now())
		if err != nil {
			if err == repository.ErrAsrQuotaExceeded {
				c.JSON(http.StatusPaymentRequired, gin.H{"code": 3004, "message": "本月语音识别额度已用完"})
			} else {
				h.logger.Warn("ASR 额度校验失败", zap.Error(err), zap.String("user_id", uid))
				c.JSON(http.StatusInternalServerError, gin.H{"code": 1000, "message": "额度校验失败: " + err.Error()})
			}
			return
		}
	}

	language := c.DefaultPostForm("language", "zh")

	result, err := h.sttService.Transcribe(file, fileHeader.Filename, fileHeader.Size, language)
	if err != nil {
		h.logger.Warn("语音识别失败", zap.Error(err), zap.String("filename", fileHeader.Filename))
		c.JSON(http.StatusInternalServerError, gin.H{"code": 3001, "message": "语音识别失败: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    result,
	})
}
