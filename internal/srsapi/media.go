package srsapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Track 是 SRS 上报的一路流（video 或 audio）的编码信息。
type Track struct {
	Codec      string `json:"codec"`
	Profile    string `json:"profile"`
	Level      string `json:"level"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	SampleRate int    `json:"sample_rate"`
	Channel    int    `json:"channel"`
}

// Stream 是 /api/v1/streams/ 返回的一条推流记录，只保留业务用得上的字段。
type Stream struct {
	App     string `json:"app"`
	Name    string `json:"name"`
	Clients int    `json:"clients"`
	Publish struct {
		Active bool `json:"active"`
	} `json:"publish"`
	Video Track `json:"video"`
	Audio Track `json:"audio"`
}

const (
	// defaultStreamsTTL 是编码信息的缓存时长：推流中编码不会突变，
	// 而每来一个观众都会请求一次播放信息，没必要每次都打 SRS。
	defaultStreamsTTL = 2 * time.Second
	// defaultStreamsTimeout 是单次查询的上限。它必须是"短超时"：
	// 编码信息只是增强提示，SRS 挂了也不能让播放接口跟着变慢。
	defaultStreamsTimeout = 800 * time.Millisecond
	// streamsCacheLimit 超过后整体清空，避免无人观看的房间长期占用缓存。
	streamsCacheLimit = 256
)

// StreamInspector 通过 SRS 的 /api/v1/streams/ 查询推流的编码参数。
//
// 存在的理由：SRS 6 的 rtmp_to_rtc 不做转码。浏览器只 offer H.264，
// 若主播用 H.265 推流，SDP 协商依旧成功，却永远收不到视频 RTP——
// 浏览器侧表现为"连上了但一直黑屏"且没有任何报错。
// 拿到 video.codec 后，后端就能在播放前明确告诉前端"这个源浏览器解不了"。
//
// 该能力可选：未配置地址时所有方法返回零值，业务链路不受影响。
type StreamInspector struct {
	endpoint string
	username string
	password string
	hc       *http.Client
	ttl      time.Duration

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	stream Stream
	found  bool
	at     time.Time
}

// NewStreamInspector 创建查询器；endpoint 形如 http://srs:1985/api/v1/streams/。
func NewStreamInspector(endpoint, username, password string) *StreamInspector {
	return &StreamInspector{
		endpoint: strings.TrimRight(endpoint, "/") + "/",
		username: username,
		password: password,
		hc:       &http.Client{Timeout: defaultStreamsTimeout},
		ttl:      defaultStreamsTTL,
		cache:    make(map[string]cacheEntry),
	}
}

type streamsResponse struct {
	Code    int      `json:"code"`
	Streams []Stream `json:"streams"`
}

// Stream 返回指定 app + 流名（即用户名）的编码信息；该流不在线时 ok 为 false。
func (i *StreamInspector) Stream(ctx context.Context, app, name string) (Stream, bool) {
	if i == nil || i.endpoint == "" {
		return Stream{}, false
	}

	key := app + "/" + name
	i.mu.Lock()
	if e, ok := i.cache[key]; ok && time.Since(e.at) < i.ttl {
		i.mu.Unlock()
		return e.stream, e.found
	}
	i.mu.Unlock()

	streams, err := i.Streams(ctx)
	if err != nil {
		return Stream{}, false
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.cache) > streamsCacheLimit {
		i.cache = make(map[string]cacheEntry)
	}
	now := time.Now()
	for _, s := range streams {
		i.cache[s.App+"/"+s.Name] = cacheEntry{stream: s, found: true, at: now}
	}
	if _, ok := i.cache[key]; !ok {
		i.cache[key] = cacheEntry{at: now}
	}
	e := i.cache[key]
	return e.stream, e.found
}

// Streams 返回 SRS 当前全部推流。注意 SRS 的 /api/v1/streams/ 不支持按流名过滤，
// 过滤只能在调用方做。
func (i *StreamInspector) Streams(ctx context.Context) ([]Stream, error) {
	if i == nil || i.endpoint == "" {
		return nil, fmt.Errorf("srsapi: 未配置 streams 接口地址")
	}

	ctx, cancel := context.WithTimeout(ctx, defaultStreamsTimeout)
	defer cancel()

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
		return nil, fmt.Errorf("srsapi: 查询推流列表失败，HTTP %d", resp.StatusCode)
	}

	var payload streamsResponse
	dec := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("srsapi: 解析推流列表失败: %w", err)
	}
	if payload.Code != 0 {
		return nil, fmt.Errorf("srsapi: SRS 返回错误码 %d", payload.Code)
	}
	return payload.Streams, nil
}

// StreamsURLOf 从任意 SRS API 地址推导 /api/v1/streams/ 地址。
func StreamsURLOf(raw string) string {
	base := BaseOf(raw)
	if base == "" {
		return ""
	}
	return base + "/api/v1/streams/"
}
