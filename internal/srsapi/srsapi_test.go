package srsapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Clients 应能解析 SRS /api/v1/clients/ 的响应。
func TestClients(t *testing.T) {
	var gotAuth bool
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/clients/" {
			t.Errorf("请求路径不正确: %s", r.URL.Path)
		}
		if user, pass, ok := r.BasicAuth(); ok {
			gotAuth = user == "admin" && pass == "secret"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"clients": []map[string]any{
				{"id": "c1", "app": "live", "stream": "alice", "publish": true},
				{"id": "v1", "app": "live", "stream": "alice", "publish": false},
			},
		})
	}))
	defer ts.Close()

	inspector := New(ts.URL+"/api/v1/clients/", "admin", "secret")
	clients, err := inspector.Clients(context.Background())
	if err != nil {
		t.Fatalf("查询客户端列表失败: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("应返回 2 条客户端，实际 %d", len(clients))
	}
	if clients[0].ID != "c1" || !clients[0].Publish {
		t.Fatalf("客户端解析错误: %+v", clients[0])
	}
	if !gotAuth {
		t.Fatal("配置了账号密码时应发送 BasicAuth")
	}
}

// SRS 返回非 0 的 code 时视为失败。
func TestClientsErrorCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1})
	}))
	defer ts.Close()

	if _, err := New(ts.URL+"/api/v1/clients/", "", "").Clients(context.Background()); err == nil {
		t.Fatal("错误码应导致查询失败")
	}
}

// HTTP 非 200 时视为失败。
func TestClientsHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	if _, err := New(ts.URL+"/api/v1/clients/", "", "").Clients(context.Background()); err == nil {
		t.Fatal("401 应导致查询失败")
	}
}

// BaseOf 从完整地址里取出 origin。
func TestBaseOf(t *testing.T) {
	if got := BaseOf("http://127.0.0.1:1985/api/v1/clients/"); got != "http://127.0.0.1:1985" {
		t.Fatalf("origin 提取错误: %q", got)
	}
	if got := BaseOf("::not a url"); got != "" {
		t.Fatalf("非法地址应返回空串，实际 %q", got)
	}
}
