package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eleball/gateway/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPDriver_Execute 验证 mcp driver 能通过 Streamable HTTP JSON-RPC 调用工具。
func TestMCPDriver_Execute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string                 `json:"name"`
				Arguments map[string]interface{} `json:"arguments"`
			} `json:"params"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		w.Header().Set("Content-Type", "application/json")
		if req.Method == "tools/call" && req.Params.Name == "hello" {
			name := req.Params.Arguments["name"]
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"jsonrpc": "2.0",
				"id":      1,
				"result": map[string]interface{}{
					"content": []map[string]string{
						{"type": "text", "text": fmt.Sprintf("Hello, %s!", name)},
					},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"isError": true,
				"content": []map[string]string{
					{"type": "text", "text": "not found"},
				},
			},
		})
	}))
	defer server.Close()

	driver := NewMCPDriver(nil)
	configJSON, _ := json.Marshal(&model.MCPServerConfig{URL: server.URL + "/mcp"})
	result, err := driver.Execute(context.Background(), "hello", map[string]interface{}{
		"__mcp_server__": string(configJSON),
		"params":         map[string]interface{}{"name": "Eleball"},
	}, &ToolEnv{})
	require.NoError(t, err)
	assert.Equal(t, "Hello, Eleball!", extractMCPContentText(result))
}

// TestMCPDriver_ToolErrorMapping 验证 MCP result 中 isError=true 映射为 ToolResult.Error。
func TestMCPDriver_ToolErrorMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"isError": true,
				"content": []map[string]string{
					{"type": "text", "text": "credential missing"},
				},
			},
		})
	}))
	defer server.Close()

	driver := NewMCPDriver(nil)
	configJSON, _ := json.Marshal(&model.MCPServerConfig{URL: server.URL})
	result, err := driver.Execute(context.Background(), "hello", map[string]interface{}{
		"__mcp_server__": string(configJSON),
	}, &ToolEnv{})
	require.NoError(t, err)
	assert.Equal(t, "tool_error", result["error_code"])
	assert.Contains(t, result["error"], "credential missing")
}

// TestMCPDriver_UnknownTool 验证 MCP 返回 isError 时映射为 ToolResult.Error。
func TestMCPDriver_UnknownTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"isError": true,
				"content": []map[string]string{
					{"type": "text", "text": "unknown tool"},
				},
			},
		})
	}))
	defer server.Close()

	driver := NewMCPDriver(nil)
	configJSON, _ := json.Marshal(&model.MCPServerConfig{URL: server.URL})
	result, err := driver.Execute(context.Background(), "unknown", map[string]interface{}{
		"__mcp_server__": string(configJSON),
	}, &ToolEnv{})
	require.NoError(t, err)
	assert.Equal(t, "tool_error", result["error_code"])
	assert.Contains(t, result["error"], "unknown tool")
}
