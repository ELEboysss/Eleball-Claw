package handler

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/eleball/gateway/internal/service"
	"github.com/gin-gonic/gin"
)

// AgentWorkflowHandler Agent 工作流处理器
type AgentWorkflowHandler struct {
	agentService *service.AgentService
	// claw：搜索能力下沉到 search-web 模块，注入后 ListSearchProviders 优先转发模块 list_sources
	moduleRegistry *service.ModuleRegistry
}

// NewAgentWorkflowHandler 创建处理器
func NewAgentWorkflowHandler(agentService *service.AgentService) *AgentWorkflowHandler {
	return &AgentWorkflowHandler{agentService: agentService}
}

// SetModuleRegistry 注入模块注册表（claw 用：search-providers 转发 search-web 模块）。
// 不改构造签名以保持向后兼容；未注入时 ListSearchProviders 回退环境变量。
func (h *AgentWorkflowHandler) SetModuleRegistry(r *service.ModuleRegistry) {
	h.moduleRegistry = r
}

// getUserID 从 gin context 获取当前用户 ID
func (h *AgentWorkflowHandler) getUserID(c *gin.Context) (string, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return "", false
	}
	s, ok := userID.(string)
	return s, ok
}

// Execute 执行 Agent 工作流（SSE）
func (h *AgentWorkflowHandler) Execute(c *gin.Context) {
	var req service.AgentExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1001, "message": "参数错误: " + err.Error()})
		return
	}

	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}
	ctx := context.WithValue(c.Request.Context(), "user_id", userID)

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	// 调用 service 执行，使用 gin.ResponseWriter 作为 SSE writer
	_ = h.agentService.Execute(ctx, req, c.Writer)
}

// ListSessions 查询当前用户的 Agent Session 列表
func (h *AgentWorkflowHandler) ListSessions(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	items, total, err := h.agentService.ListSessions(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    gin.H{"total": total, "items": items},
	})
}

// GetSession 查询 Agent Session 详情
func (h *AgentWorkflowHandler) GetSession(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	id := c.Param("id")
	item, err := h.agentService.GetSession(c.Request.Context(), id, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success", "data": item})
}

// DeleteSession 删除 Agent Session 及其磁盘资源
func (h *AgentWorkflowHandler) DeleteSession(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	id := c.Param("id")
	if err := h.agentService.DeleteSession(c.Request.Context(), id, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// DeleteSessions 批量删除 Agent Session 及其资源
// - 带 conversation_id 查询参数：删除该对话下的所有 Session
// - 不带查询参数：删除当前用户的全部 Session
func (h *AgentWorkflowHandler) DeleteSessions(c *gin.Context) {
	userID, ok := h.getUserID(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": 2001, "message": "未登录"})
		return
	}

	conversationID := c.Query("conversation_id")
	var err error
	if conversationID != "" {
		err = h.agentService.DeleteSessionsByConversation(c.Request.Context(), userID, conversationID)
	} else {
		err = h.agentService.DeleteAllSessions(c.Request.Context(), userID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"code": 5000, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "success"})
}

// ListSearchProviders 返回当前已配置的可用搜索源列表
// 前端据此动态渲染搜索源下拉框，未配置 key 的源不展示。
//
// claw：搜索能力下沉到 search-web 模块（源配置在模块容器侧），优先转发模块的
// list_sources 取真实可用源；模块离线或未注入 registry 时回退读 gateway 环境变量。
func (h *AgentWorkflowHandler) ListSearchProviders(c *gin.Context) {
	if h.moduleRegistry != nil {
		if providers, err := h.listSearchWebSources(c); err == nil && len(providers) > 0 {
			c.JSON(http.StatusOK, gin.H{
				"code":    0,
				"message": "success",
				"data":    providers,
				"source":  "search-web",
			})
			return
		}
	}
	// 回退：模块离线或未配置时，读 gateway 环境变量（云端行为）
	providers := service.ListAvailableSearchProviders()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    providers,
		"source":  "env-fallback",
	})
}

// listSearchWebSources 调 search-web 模块的 list_sources，过滤可用源并映射为
// [{name,label}]（对齐 ListAvailableSearchProviders 契约，前端无需改动）。
func (h *AgentWorkflowHandler) listSearchWebSources(c *gin.Context) ([]gin.H, error) {
	userID, _ := h.getUserID(c)
	result, err := h.moduleRegistry.Execute("search-web", "list_sources", map[string]interface{}{}, userID)
	if err != nil {
		return nil, err
	}
	sourcesRaw, ok := result["sources"]
	if !ok {
		return nil, errors.New("search-web 未返回 sources")
	}
	arr, ok := sourcesRaw.([]interface{})
	if !ok {
		return nil, errors.New("search-web sources 格式异常")
	}
	out := make([]gin.H, 0, len(arr))
	for _, s := range arr {
		m, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		// 仅返回 available=true 的源（未配置凭据的不展示）
		if avail, _ := m["available"].(bool); !avail {
			continue
		}
		name, _ := m["name"].(string)
		label, _ := m["label"].(string)
		if name == "" {
			continue
		}
		out = append(out, gin.H{"name": name, "label": label})
	}
	return out, nil
}

// GetResource 匿名代理下载 Agent 输出资源
func (h *AgentWorkflowHandler) GetResource(c *gin.Context) {
	id := c.Param("id")
	data, mimeType, fileName, err := h.agentService.GetResource(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 4004, "message": err.Error()})
		return
	}

	if mimeType != "" {
		c.Header("Content-Type", mimeType)
	}
	if fileName != "" {
		c.Header("Content-Disposition", "attachment; filename=\""+fileName+"\"")
	}
	c.Data(http.StatusOK, mimeType, data)
}
