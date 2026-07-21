package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentWorkflow_GetSessionAndDelete(t *testing.T) {
	r, token := setupAgentWorkflowTest(t)

	// 先创建一个对话以生成 session
	body, _ := json.Marshal(map[string]interface{}{"title": "session-test", "model": "gpt-4o-mini", "provider": "openai"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/conversations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	convID := createResp["data"].(map[string]interface{})["id"].(string)

	// 调用 execute 生成 session（无 LLM 客户端，会返回错误事件但会创建对话）
	msgBody, _ := json.Marshal(map[string]interface{}{"conversation_id": convID, "message": "hello", "enable_tools": true})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/v1/agent/execute", bytes.NewBuffer(msgBody))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	// 查询 session 列表
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("GET", "/v1/agent/sessions", nil)
	req3.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusOK, w3.Code)
	var listResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &listResp))
	data := listResp["data"].(map[string]interface{})
	total := int(data["total"].(float64))
	require.GreaterOrEqual(t, total, 1)
	items := data["items"].([]interface{})
	sessionID := items[0].(map[string]interface{})["id"].(string)

	// 查询 session 详情
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/v1/agent/sessions/"+sessionID, nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w4, req4)
	require.Equal(t, http.StatusOK, w4.Code)
	var getResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &getResp))
	assert.Equal(t, float64(0), getResp["code"])
	assert.NotNil(t, getResp["data"])

	// 删除 session
	w5 := httptest.NewRecorder()
	req5, _ := http.NewRequest("DELETE", "/v1/agent/sessions/"+sessionID, nil)
	req5.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w5, req5)
	require.Equal(t, http.StatusOK, w5.Code)
	var deleteResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w5.Body.Bytes(), &deleteResp))
	assert.Equal(t, float64(0), deleteResp["code"])
}
