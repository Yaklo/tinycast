package mtx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamNameRoundTrip(t *testing.T) {
	name := StreamName("live", "alex")
	if name != "live/alex" {
		t.Fatalf("流名应为 live/alex，实际 %s", name)
	}
	app, username := SplitStreamName(name)
	if app != "live" || username != "alex" {
		t.Fatalf("拆分结果异常：app=%s username=%s", app, username)
	}
	// 用户名里不允许斜杠，因此按最后一个斜杠切分是安全的。
	app, username = SplitStreamName("live/team/alex")
	if app != "live/team" || username != "alex" {
		t.Fatalf("多级应用名拆分异常：app=%s username=%s", app, username)
	}
	if app, username := SplitStreamName("alex"); app != "" || username != "alex" {
		t.Fatalf("无应用名时应返回空 app，实际 app=%s username=%s", app, username)
	}
}

func TestQueryValue(t *testing.T) {
	cases := map[string]struct {
		raw  string
		key  string
		want string
	}{
		"带问号":    {"?room=%E6%B5%8B%E8%AF%95&token=abc", "room", "测试"},
		"不带问号":   {"room=测试直播&token=abc", "room", "测试直播"},
		"参数在中部":  {"a=1&token=abc&b=2", "token", "abc"},
		"参数不存在":  {"a=1", "token", ""},
		"空字符串":   {"", "token", ""},
		"百分号未编码": {"room=%zz&token=abc", "token", "abc"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := QueryValue(tc.raw, tc.key); got != tc.want {
				t.Fatalf("QueryValue(%q, %q) = %q，期望 %q", tc.raw, tc.key, got, tc.want)
			}
		})
	}
}

func TestDecodeRoomValue(t *testing.T) {
	cases := map[string]string{
		"测试直播":                           "测试直播",
		"%E6%B5%8B%E8%AF%95":             "测试",
		"%25E6%25B5%258B%25E8%25AF%2595": "测试",
		"100%":                           "100%",
	}
	for raw, want := range cases {
		if got := DecodeRoomValue(raw); got != want {
			t.Fatalf("DecodeRoomValue(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

func TestClientGlobalConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config/global/get" {
			t.Errorf("请求路径异常：%s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"authHTTPAddress\":\"http://127.0.0.1:18855/internal/mtx/auth\"}"))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "", "").GlobalConfig(context.Background())
	if err != nil {
		t.Fatalf("GlobalConfig: %v", err)
	}
	if got.AuthHTTPAddress != "http://127.0.0.1:18855/internal/mtx/auth" {
		t.Fatalf("回调地址解析错误：%q", got.AuthHTTPAddress)
	}
}

const pathsBody = `{
  "itemCount": 1,
  "pageCount": 1,
  "items": [
    {
      "name": "live/alex",
      "confName": "all_others",
      "ready": true,
      "tracks": ["H264", "MPEG-4 Audio"],
      "tracks2": [
        {"codec": "H264", "codecProps": {"width": 1920, "height": 1080, "profile": "High", "level": "4.1"}},
        {"codec": "MPEG-4 Audio", "codecProps": {"sampleRate": 48000, "channelCount": 2}}
      ],
      "readers": [{"type": "hlsSession", "id": "r1"}, {"type": "webRTCSession", "id": "r2"}],
      "source": {"type": "rtmpConn", "id": "s1"}
    }
  ]
}`

func TestClientPaths(t *testing.T) {
	var gotAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/paths/list" {
			t.Errorf("请求路径异常：%s", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		gotAuth = ok && user == "api" && pass == "secret"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pathsBody))
	}))
	defer srv.Close()

	paths, err := New(srv.URL, "api", "secret").Paths(context.Background())
	if err != nil {
		t.Fatalf("Paths: %v", err)
	}
	if !gotAuth {
		t.Fatal("配置了账号时应带 Basic 认证头")
	}
	path, ok := paths["live/alex"]
	if !ok {
		t.Fatalf("应解析出 live/alex，实际 %v", paths)
	}
	if !path.Ready {
		t.Fatal("路径应为 ready")
	}
	if len(path.Tracks2) != 2 {
		t.Fatalf("应解析出 2 条轨道，实际 %d", len(path.Tracks2))
	}
	if path.Tracks2[0].CodecProps.Width != 1920 || path.Tracks2[0].CodecProps.Profile != "High" {
		t.Fatalf("视频参数解析错误：%+v", path.Tracks2[0].CodecProps)
	}
	if path.Tracks2[1].CodecProps.SampleRate != 48000 {
		t.Fatalf("音频参数解析错误：%+v", path.Tracks2[1].CodecProps)
	}
	if len(path.Readers) != 2 || path.Readers[0].Type != ReaderHLS || path.Readers[1].Type != ReaderWebRTC {
		t.Fatalf("观看会话解析错误：%+v", path.Readers)
	}
	if path.Source == nil || path.Source.Type != "rtmpConn" {
		t.Fatalf("推流来源解析错误：%+v", path.Source)
	}
}

func TestClientPathsRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", "").Paths(context.Background()); err == nil {
		t.Fatal("非 200 应报错")
	}
}

func TestClientPathsHandlesBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", "").Paths(context.Background()); err == nil {
		t.Fatal("响应不是 JSON 时应报错")
	}
}
