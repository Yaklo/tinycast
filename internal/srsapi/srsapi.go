// Package srsapi 是 SRS HTTP API 的最小客户端。
//
// 它只用来做一件事：查询 SRS 当前在线的客户端列表，供后台清理任务核对
// "后端认为还活着" 的推流/观看会话是否真的还在。
// 这一步是可选的增强能力——未配置 SRS_CLIENTS_API_URL 时整个功能关闭，
// 房间与人数完全依赖 on_publish / on_unpublish / on_play / on_stop 回调。
package srsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是 SRS 在线客户端的一条记录。
type Client struct {
	ID      string `json:"id"`
	App     string `json:"app"`
	Stream  string `json:"stream"`
	Publish bool   `json:"publish"`
	Type    string `json:"type"`
}

// Inspector 查询 SRS 的 /api/v1/clients/ 接口。
type Inspector struct {
	endpoint string
	username string
	password string
	hc       *http.Client
}

// New 创建 Inspector；endpoint 形如 http://srs:1985/api/v1/clients/。
func New(endpoint, username, password string) *Inspector {
	return &Inspector{
		endpoint: strings.TrimRight(endpoint, "/") + "/",
		username: username,
		password: password,
		hc:       &http.Client{Timeout: 3 * time.Second},
	}
}

type clientsResponse struct {
	Code    int      `json:"code"`
	Clients []Client `json:"clients"`
}

// Clients 返回 SRS 当前的在线客户端。
func (i *Inspector) Clients(ctx context.Context) ([]Client, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, i.endpoint, nil)
	if err != nil {
		return nil, err
	}
	if i.username != "" || i.password != "" {
		req.SetBasicAuth(i.username, i.password)
	}
	resp, err := i.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("srsapi: 查询客户端列表失败，HTTP %d", resp.StatusCode)
	}

	var payload clientsResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("srsapi: 解析客户端列表失败: %w", err)
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("srsapi: SRS 返回错误码 %d", payload.Code)
	}
	return payload.Clients, nil
}

// BaseOf 从完整 API 地址里取出 origin，便于拼接其它接口。
func BaseOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
