package middleware

import (
	"net/http"
	"sync/atomic"

	"github.com/gin-gonic/gin"
)

// adminEnabled 管理后台运行时开关，1 表示开启，0 表示关闭。
// 默认开启，与 start.sh 中 --enable-admin 的启动策略保持一致；
// 可通过 /_internal/admin/:action 接口在运行时动态关闭或重新开启。
var adminEnabled int32 = 1

// SetAdminEnabled 设置管理后台运行时开关状态。
func SetAdminEnabled(enabled bool) {
	if enabled {
		atomic.StoreInt32(&adminEnabled, 1)
	} else {
		atomic.StoreInt32(&adminEnabled, 0)
	}
}

// IsAdminEnabled 返回当前管理后台是否处于开启状态。
func IsAdminEnabled() bool {
	return atomic.LoadInt32(&adminEnabled) == 1
}

// AdminSwitch 管理后台动态开关中间件。
// 当开关关闭时，所有 /v1/admin/* 请求返回 404，隐藏管理后台接口存在性。
func AdminSwitch() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !IsAdminEnabled() {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"code": 404, "message": "not found"})
			return
		}
		c.Next()
	}
}
