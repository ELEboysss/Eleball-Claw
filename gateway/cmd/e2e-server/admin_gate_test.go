package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/cookiejar"
	"testing"
)

// setupGateServer 构造一个最小 e2e 闸门测试服务器（闸门三端点 + 一个受保护的 /v1/admin/test）。
func setupGateServer(t *testing.T, enabled bool, token string) (*e2eAdminGate, *httptest.Server) {
	t.Helper()
	if enabled {
		t.Setenv("ADMIN_GATE_ENABLED", "true")
		t.Setenv("ADMIN_GATE_TOKEN", token)
	} else {
		t.Setenv("ADMIN_GATE_ENABLED", "false")
	}
	gate := newE2EAdminGate()
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/knock", gate.knockPageHandler)
	mux.HandleFunc("/_admin_gate", gate.verifyHandler)
	mux.HandleFunc("/_admin_gate_check", gate.checkHandler)
	mux.HandleFunc("/v1/admin/test", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(gate.Middleware(mux))
	t.Cleanup(srv.Close)
	return gate, srv
}

// newGateClient 构造带 cookie jar 的 HTTP 客户端（自动管理 cookie）。
func newGateClient(t *testing.T) *http.Client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

func postGate(t *testing.T, client *http.Client, url, token string) (int, map[string]interface{}) {
	body, _ := json.Marshal(map[string]string{"token": token})
	resp, err := client.Post(url+"/_admin_gate", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestE2EGate_BlockWithoutCookie 闸门启用时，无 cookie 访问 /v1/admin/* 应 401。
func TestE2EGate_BlockWithoutCookie(t *testing.T) {
	_, srv := setupGateServer(t, true, "e2e-test-token")
	client := newGateClient(t) // 空 jar，无 cookie

	resp, err := client.Get(srv.URL + "/v1/admin/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 cookie 应被闸门拦截返回 401，实际 %d", resp.StatusCode)
	}
}

// TestE2EGate_VerifyAndPass 正确口令验证后，带 cookie 访问 /v1/admin/* 应通过。
func TestE2EGate_VerifyAndPass(t *testing.T) {
	_, srv := setupGateServer(t, true, "e2e-test-token")
	client := newGateClient(t)

	// 1. 提交正确口令
	code, out := postGate(t, client, srv.URL, "e2e-test-token")
	if code != http.StatusOK || out["code"] != float64(0) {
		t.Fatalf("正确口令应返回 200/code=0，实际 %d %v", code, out)
	}

	// 2. 带 cookie（jar 自动）访问受保护接口
	resp, err := client.Get(srv.URL + "/v1/admin/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("验证后应放行，实际 %d %s", resp.StatusCode, body)
	}
}

// TestE2EGate_CheckEndpoint /_admin_gate_check 状态码：带 cookie 200，不带 401。
func TestE2EGate_CheckEndpoint(t *testing.T) {
	_, srv := setupGateServer(t, true, "e2e-test-token")
	authedClient := newGateClient(t)
	postGate(t, authedClient, srv.URL, "e2e-test-token") // 设 cookie

	// 带 cookie
	resp, _ := authedClient.Get(srv.URL + "/_admin_gate_check")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("带 cookie 的 check 应 200，实际 %d", resp.StatusCode)
	}

	// 不带 cookie（新客户端）
	bareClient := &http.Client{}
	resp2, _ := bareClient.Get(srv.URL + "/_admin_gate_check")
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("不带 cookie 的 check 应 401，实际 %d", resp2.StatusCode)
	}
}

// TestE2EGate_WrongToken 错误口令应 401 且不设 cookie（后续仍被拦截）。
func TestE2EGate_WrongToken(t *testing.T) {
	_, srv := setupGateServer(t, true, "e2e-test-token")
	client := newGateClient(t)

	code, out := postGate(t, client, srv.URL, "wrong-token")
	if code != http.StatusUnauthorized || out["code"] != float64(1001) {
		t.Fatalf("错误口令应 401/code=1001，实际 %d %v", code, out)
	}

	// 错误口令后仍无有效 cookie，受保护接口应 401
	resp, _ := client.Get(srv.URL + "/v1/admin/test")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误口令后应仍被拦截 401，实际 %d", resp.StatusCode)
	}
}

// TestE2EGate_KnockPage /admin/knock 返回 HTML 输入页。
func TestE2EGate_KnockPage(t *testing.T) {
	_, srv := setupGateServer(t, true, "e2e-test-token")
	resp, err := http.Get(srv.URL + "/admin/knock")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("knock 页应为 text/html，实际 %s", resp.Header.Get("Content-Type"))
	}
	if !bytes.Contains(body, []byte("/_admin_gate")) {
		t.Fatalf("knock 页应含 /_admin_gate 提交地址")
	}
}

// TestE2EGate_Disabled 闸门禁用时，无 cookie 也应放行 /v1/admin/*。
func TestE2EGate_Disabled(t *testing.T) {
	_, srv := setupGateServer(t, false, "")
	bareClient := &http.Client{}

	resp, err := bareClient.Get(srv.URL + "/v1/admin/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("闸门禁用时应放行，实际 %d", resp.StatusCode)
	}
}
