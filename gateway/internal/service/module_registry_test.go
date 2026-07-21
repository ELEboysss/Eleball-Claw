package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eleball/gateway/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newModuleTestServer 创建一个模拟的 Agent-Reach 模块 HTTP 服务
func newModuleTestServer(t *testing.T, online bool, executeFunc func(action string, params map[string]interface{}, userID string) map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			if !online {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte(`{"status":"error"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"module_id":    "agent-reach",
				"version":      "1.0.0",
				"status":       "ok",
				"capabilities": []string{"web_read", "search"},
			})
		case "/execute":
			var req struct {
				Action  string                 `json:"action"`
				Params  map[string]interface{} `json:"params"`
				UserID  string                 `json:"user_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			result := map[string]interface{}{"content": "ok"}
			if executeFunc != nil {
				result = executeFunc(req.Action, req.Params, req.UserID)
			}
			_ = json.NewEncoder(w).Encode(result)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestModuleRegistry_RegisterAndCheck(t *testing.T) {
	server := newModuleTestServer(t, true, nil)
	defer server.Close()

	reg := NewModuleRegistry(&config.AgentReachConfig{
		ModuleURL:           server.URL,
		HealthCheckInterval: 1 * time.Millisecond,
	})
	reg.Register("agent-reach", server.URL)

	st := reg.Check("agent-reach")
	require.NotNil(t, st)
	assert.True(t, st.Online)
	assert.Equal(t, "agent-reach", st.ModuleID)
	assert.Equal(t, "1.0.0", st.Version)
	assert.ElementsMatch(t, []string{"web_read", "search"}, st.Capabilities)
}

func TestModuleRegistry_CheckOffline(t *testing.T) {
	server := newModuleTestServer(t, false, nil)
	defer server.Close()

	reg := NewModuleRegistry(&config.AgentReachConfig{
		ModuleURL:           server.URL,
		HealthCheckInterval: 1 * time.Millisecond,
	})
	reg.Register("agent-reach", server.URL)

	st := reg.Check("agent-reach")
	require.NotNil(t, st)
	assert.False(t, st.Online)
	assert.NotEmpty(t, st.Error)
}

func TestModuleRegistry_CheckUnregistered(t *testing.T) {
	reg := NewModuleRegistry(&config.AgentReachConfig{})
	assert.Nil(t, reg.Check("not-exist"))
}

func TestModuleRegistry_List(t *testing.T) {
	server := newModuleTestServer(t, true, nil)
	defer server.Close()

	reg := NewModuleRegistry(&config.AgentReachConfig{
		ModuleURL:           server.URL,
		HealthCheckInterval: 1 * time.Millisecond,
	})
	reg.Register("agent-reach", server.URL)

	list := reg.List()
	require.Len(t, list, 1)
	assert.Equal(t, "agent-reach", list[0].ModuleID)
	assert.True(t, list[0].Online)
}

func TestModuleRegistry_Execute(t *testing.T) {
	server := newModuleTestServer(t, true, func(action string, params map[string]interface{}, userID string) map[string]interface{} {
		return map[string]interface{}{
			"action":   action,
			"query":    params["query"],
			"user_id":  userID,
			"content":  "result",
		}
	})
	defer server.Close()

	reg := NewModuleRegistry(&config.AgentReachConfig{
		ModuleURL:           server.URL,
		HealthCheckInterval: 1 * time.Millisecond,
	})
	reg.Register("agent-reach", server.URL)

	result, err := reg.Execute("agent-reach", "web_read", map[string]interface{}{"query": "https://example.com"}, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "web_read", result["action"])
	assert.Equal(t, "https://example.com", result["query"])
	assert.Equal(t, "user-1", result["user_id"])
}

func TestModuleRegistry_ExecuteModuleError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"module_id":"agent-reach","version":"1.0.0","status":"ok","capabilities":[]}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"module internal error"}`))
	}))
	defer server.Close()

	reg := NewModuleRegistry(&config.AgentReachConfig{
		ModuleURL:           server.URL,
		HealthCheckInterval: 1 * time.Millisecond,
	})
	reg.Register("agent-reach", server.URL)

	_, err := reg.Execute("agent-reach", "web_read", map[string]interface{}{"query": "https://example.com"}, "user-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "模块返回 HTTP 500")
}

func TestModuleRegistry_ModuleURL(t *testing.T) {
	reg := NewModuleRegistry(&config.AgentReachConfig{ModuleURL: "http://custom:9000"})
	assert.Equal(t, "http://custom:9000", reg.ModuleURL("agent-reach"))
	assert.Equal(t, "http://other:8080", reg.ModuleURL("other"))
}

func TestModuleRegistry_CheckCache(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			callCount++
			_, _ = fmt.Fprintf(w, `{"module_id":"agent-reach","version":"1.0.%d","status":"ok","capabilities":[]}`, callCount)
		}
	}))
	defer server.Close()

	reg := NewModuleRegistry(&config.AgentReachConfig{
		ModuleURL:           server.URL,
		HealthCheckInterval: 1 * time.Hour,
	})
	reg.Register("agent-reach", server.URL)

	_ = reg.Check("agent-reach")
	_ = reg.Check("agent-reach")
	assert.Equal(t, 1, callCount)
}
