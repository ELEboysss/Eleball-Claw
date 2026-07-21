package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// Logger Zap 日志中间件，自动注入 trace_id
func Logger(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		traceID := uuid.New().String()
		c.Set("trace_id", traceID)
		c.Writer.Header().Set("X-Trace-ID", traceID)

		c.Next()

		latency := time.Since(start)
		log.Info("http request",
			zap.String("trace_id", traceID),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", c.Writer.Status()),
			zap.Duration("latency", latency),
			zap.String("client_ip", c.ClientIP()),
		)
	}
}
