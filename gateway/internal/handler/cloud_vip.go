package handler

import (
	"net/http"
	"strings"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// requireCloudVIP1 校验当前请求用户的云端 VIP >= 1（claw 云端秘技门控）。
//
// cloudAccount 为 nil（云端 cmd/server）时直接放行--云端走自身购买/VIP 流程。
// 返回 true 表示通过（或无需校验）；返回 false 表示已写入错误响应，调用方应 return。
func requireCloudVIP1(c *gin.Context, cloudAccount *service.CloudAccountService) bool {
	if cloudAccount == nil {
		return true
	}
	userIDVal, _ := c.Get("user_id")
	uid, _ := userIDVal.(string)
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if err := cloudAccount.RequireVIP1(c.Request.Context(), uid, token); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"code": 4002, "message": err.Error()})
		return false
	}
	return true
}
