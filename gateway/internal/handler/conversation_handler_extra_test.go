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

func TestConversation_GetAndUpdate(t *testing.T) {
	r, _, token := setupConversationTest(t)

	body, _ := json.Marshal(map[string]interface{}{"title": "update-test", "model": "gpt-4o-mini", "provider": "openai"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/conversations", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var createResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &createResp))
	convID := createResp["data"].(map[string]interface{})["id"].(string)

	// 查询详情
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/v1/conversations/"+convID, nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	var getResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &getResp))
	assert.Equal(t, float64(0), getResp["code"])
	assert.Equal(t, "update-test", getResp["data"].(map[string]interface{})["title"])

	// 更新标题
	updateBody, _ := json.Marshal(map[string]interface{}{"title": "updated-title"})
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest("PATCH", "/v1/conversations/"+convID, bytes.NewBuffer(updateBody))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusOK, w3.Code)
	var updateResp map[string]interface{}
	require.NoError(t, json.Unmarshal(w3.Body.Bytes(), &updateResp))
	assert.Equal(t, float64(0), updateResp["code"])

	// 再次查询确认更新
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest("GET", "/v1/conversations/"+convID, nil)
	req4.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(w4, req4)
	require.Equal(t, http.StatusOK, w4.Code)
	var getResp2 map[string]interface{}
	require.NoError(t, json.Unmarshal(w4.Body.Bytes(), &getResp2))
	assert.Equal(t, "updated-title", getResp2["data"].(map[string]interface{})["title"])
}
