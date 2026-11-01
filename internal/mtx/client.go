// Package mtx 是 MediaMTX 控制接口（/v3）的最小客户端。
//
// tinycast 只依赖一个事实来源：MediaMTX 自己的推流清单。
// 房间状态、编码参数、观看会话数都从这里读，不再维护第二套"影子状态"，
// 因此进程重启、MediaMTX 重启都不会产生不一致。
package mtx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 观看会话类型（MediaMTX 的 readers[].type 取值）。
const (
	ReaderWebRTC = "webRTCSession"
	ReaderHLS    = "hlsSession"
)

// 默认请求超时：巡检是一秒一次的高频调用，不能因为上游卡住而堆积 goroutine。
const defaultTimeout = 3 * time.Second

// TrackProps 是轨道编码参数，字段随编码格式不同而不同。
type TrackProps struct {
	// 视频
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Profile string `json:"profile,omitempty"`
	Level   string `json:"level,omitempty"`
	// 音频
	SampleRate   int `json:"sampleRate,omitempty"`
	ChannelCount int `json:"channelCount,omitempty"`
}

// Track 是 MediaMTX 的 tracks2[] 元素：编码名 + 该编码的参数。
type Track struct {
	Codec      string     `json:"codec"`
	CodecProps TrackProps `json:"codecProps"`
}

// Reader 是一条观看会话。
type Reader struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Source 是推流来源（rtmpConn / webRTCSession / rtspSession ...）。
type Source struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Path 是 MediaMTX 中的一路流（路径名形如 live/alex）。
type Path struct {
	Name    string   `json:"name"`
	Ready   bool     `json:"ready"`
	Tracks  []string `json:"tracks"`
	Tracks2 []Track  `json:"tracks2"`
	Readers []Reader `json:"readers"`
	Source  *Source  `json:"source"`
}

// pathsResponse 是 GET /v3/paths/list 的响应体。
type pathsResponse struct {
	Items []Path `json:"items"`
}

// Client 访问 MediaMTX 控制接口。
type Client struct {
	base string
	user string
	pass string
	http *http.Client
}

// New 创建一个控制接口客户端。base 形如 http://127.0.0.1:9997。
func New(base, user, pass string) *Client {
	return &Client{
		base: strings.TrimRight(base, "/"),
		user: user,
		pass: pass,
		http: &http.Client{Timeout: defaultTimeout},
	}
}

// Paths 拉取当前全部推流，键是路径名（例如 live/alex）。
//
// 推流断开后该路径会整条从列表里消失，因此调用方可以直接用"键是否存在"
// 判断房间是否还在推流。
func (c *Client) Paths(ctx context.Context) (map[string]Path, error) {
	if c == nil || c.base == "" {
		return nil, fmt.Errorf("mtx: 未配置 MediaMTX 控制接口地址")
	}

	endpoint := c.base + "/v3/paths/list"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("mtx: 构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if c.user != "" {
		req.SetBasicAuth(c.user, c.pass)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mtx: 请求 %s 失败: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("mtx: 请求 %s 返回 %d", endpoint, resp.StatusCode)
	}

	var body pathsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("mtx: 解析 %s 响应失败: %w", endpoint, err)
	}

	out := make(map[string]Path, len(body.Items))
	for _, item := range body.Items {
		out[item.Name] = item
	}
	return out, nil
}

// StreamName 拼出 MediaMTX 的路径名，例如 live/alex。
func StreamName(app, username string) string {
	app = strings.Trim(app, "/")
	if app == "" {
		return username
	}
	return app + "/" + username
}

// SplitStreamName 把 live/alex 拆成 app 与用户名。没有前缀时 app 为空。
func SplitStreamName(name string) (app, username string) {
	name = strings.Trim(name, "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[:i], name[i+1:]
	}
	return "", name
}

// QueryValue 从 MediaMTX 回调给的原始查询串里取一个参数。
//
// MediaMTX 会把推流 URL 里 ? 之后的内容原样放进 query 字段，
// 并且可能保留百分号编码，因此这里做一次标准解码；解码失败时退回原值。
func QueryValue(raw, key string) string {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "?")
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		// 部分非法序列（例如裸的中文）会让 ParseQuery 整体失败，
		// 此时退化成手工按 & 与 = 切分，尽量把能读的参数读出来。
		return manualQueryValue(raw, key)
	}
	return values.Get(key)
}

func manualQueryValue(raw, key string) string {
	for _, pair := range strings.Split(raw, "&") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok || k != key {
			continue
		}
		if decoded, err := url.QueryUnescape(v); err == nil {
			return decoded
		}
		return v
	}
	return ""
}
