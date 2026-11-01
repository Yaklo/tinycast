package srsapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const streamsJSON = `{"code":0,"streams":[
  {"app":"live","name":"alice","clients":2,"publish":{"active":true},
   "video":{"codec":"H264","profile":"High","level":"3","width":1280,"height":720},
   "audio":{"codec":"AAC","sample_rate":44100,"channel":2,"profile":"LC"}},
  {"app":"live","name":"bob","clients":1,"publish":{"active":true},
   "video":{"codec":"HEVC","profile":"Main","level":"2.1","width":640,"height":360},
   "audio":{"codec":"AAC","sample_rate":48000,"channel":1,"profile":"LC"}}
]}`

func TestStreamInspectorStream(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path != "/api/v1/streams/" {
			t.Errorf("请求路径应为 /api/v1/streams/，实际 %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, streamsJSON)
	}))
	defer srv.Close()

	ins := NewStreamInspector(srv.URL+"/api/v1/streams", "", "")

	alice, ok := ins.Stream(context.Background(), "live", "alice")
	if !ok {
		t.Fatal("应找到 alice 的推流")
	}
	if alice.Video.Codec != "H264" || alice.Video.Width != 1280 || alice.Video.Profile != "High" {
		t.Fatalf("alice 编码信息解析错误: %+v", alice.Video)
	}
	if alice.Audio.Codec != "AAC" || alice.Audio.SampleRate != 44100 {
		t.Fatalf("alice 音频信息解析错误: %+v", alice.Audio)
	}

	bob, ok := ins.Stream(context.Background(), "live", "bob")
	if !ok || bob.Video.Codec != "HEVC" {
		t.Fatalf("bob 应为 HEVC，实际 %+v ok=%v", bob.Video, ok)
	}

	// 第二次查询命中缓存，不应再打 SRS。
	before := hits.Load()
	if _, ok := ins.Stream(context.Background(), "live", "alice"); !ok {
		t.Fatal("缓存命中时应返回 ok")
	}
	if hits.Load() != before {
		t.Fatalf("短时间内重复查询应命中缓存，实际请求数 %d -> %d", before, hits.Load())
	}
}

func TestStreamInspectorMissingStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, streamsJSON)
	}))
	defer srv.Close()

	ins := NewStreamInspector(srv.URL, "", "")
	if _, ok := ins.Stream(context.Background(), "live", "nobody"); ok {
		t.Fatal("不存在的流应返回 ok=false")
	}
}

func TestStreamInspectorError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ins := NewStreamInspector(srv.URL, "", "")
	if _, err := ins.Streams(context.Background()); err == nil {
		t.Fatal("HTTP 500 应返回错误")
	}
	// 出错时不影响调用方：Stream 返回零值 + false。
	if _, ok := ins.Stream(context.Background(), "live", "alice"); ok {
		t.Fatal("查询失败时 Stream 应返回 false")
	}
}

func TestStreamInspectorNil(t *testing.T) {
	var ins *StreamInspector
	if _, ok := ins.Stream(context.Background(), "live", "alice"); ok {
		t.Fatal("nil 查询器应安全返回 false")
	}
	if _, err := ins.Streams(context.Background()); err == nil {
		t.Fatal("nil 查询器应返回错误而不是 panic")
	}
}

func TestStreamInspectorTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, streamsJSON)
	}))
	defer slow.Close()

	ins := NewStreamInspector(slow.URL, "", "")
	ins.hc.Timeout = 50 * time.Millisecond

	start := time.Now()
	if _, err := ins.Streams(context.Background()); err == nil {
		t.Fatal("超时应返回错误")
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("超时保护失效，实际耗时 %s", elapsed)
	}
}

func TestStreamsURLOf(t *testing.T) {
	cases := map[string]string{
		"http://srs:1985/api/v1/clients/": "http://srs:1985/api/v1/streams/",
		"http://127.0.0.1:1985":           "http://127.0.0.1:1985/api/v1/streams/",
		"":                                "",
		"not-a-url":                       "",
	}
	for in, want := range cases {
		if got := StreamsURLOf(in); got != want {
			t.Errorf("StreamsURLOf(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// SRS 解析 RTMP URL 失败时会把查询串并进流名（name = "alex?room=..&token=.."），
// 这种流播放端访问不到，但编码信息仍应能读出来，供页面给出提示。
func TestStreamInspectorMatchesFoldedStreamName(t *testing.T) {
	const folded = `{"code":0,"streams":[
	  {"app":"live","name":"alex?room=%E6%B5%8B%E8%AF%95&token=TOK","clients":0,"publish":{"active":true},
	   "video":{"codec":"H264","profile":"High","level":"3.1","width":1920,"height":1080},
	   "audio":{"codec":"AAC","sample_rate":48000,"channel":2}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, folded)
	}))
	defer srv.Close()

	ins := NewStreamInspector(srv.URL, "", "")
	got, ok := ins.Stream(context.Background(), "live", "alex")
	if !ok {
		t.Fatal("流名带查询串时也应能按前缀匹配到")
	}
	if got.Video.Codec != "H264" || got.Video.Width != 1920 {
		t.Fatalf("编码信息解析错误: %+v", got.Video)
	}
	if _, ok := ins.Stream(context.Background(), "live", "alice"); ok {
		t.Fatal("前缀不匹配的流不应命中")
	}
}
