package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tinycast/internal/config"
	"tinycast/internal/hub"
)

// srsStreamsStub 模拟 SRS 的 /api/v1/streams/ 接口。
func srsStreamsStub(t *testing.T, codec string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":0,"streams":[{"app":"live","name":"alice",
			"video":{"codec":"`+codec+`","profile":"High","level":"3","width":1280,"height":720},
			"audio":{"codec":"AAC","sample_rate":44100,"channel":2}}]}`)
	}))
}

type roomPayload struct {
	Room  hub.RoomView `json:"room"`
	Media *mediaInfo   `json:"media"`
}

type playPayload struct {
	State hub.State  `json:"state"`
	Media *mediaInfo `json:"media"`
}

func getJSON[T any](t *testing.T, srv *Server, target string) T {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s 期望 200，实际 %d", target, rec.Code)
	}
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析 %s 响应失败: %v", target, err)
	}
	return out
}

// 房间详情与播放信息都应带上 SRS 上报的编码信息。
func TestRoomAndPlayIncludeMedia(t *testing.T) {
	stub := srsStreamsStub(t, "H264")
	defer stub.Close()

	srv, _ := newFixture(t, func(cfg *config.Config) {
		cfg.SRS.StreamsAPIURL = stub.URL
	})
	postCallback(t, srv, testSecret, publishBody("alice", "爱丽丝的直播间", aliceToken, "c1"))

	room := getJSON[roomPayload](t, srv, "/api/rooms/alice")
	if room.Media == nil {
		t.Fatal("房间详情应包含 media")
	}
	if room.Media.VideoCodec != "H264" || room.Media.Width != 1280 || room.Media.Height != 720 {
		t.Fatalf("media 内容错误: %+v", room.Media)
	}
	if room.Media.AudioCodec != "AAC" {
		t.Fatalf("音频编码错误: %+v", room.Media)
	}

	play := getJSON[playPayload](t, srv, "/api/rooms/alice/play")
	if play.State != hub.StateLive {
		t.Fatalf("应为 LIVE，实际 %s", play.State)
	}
	if play.Media == nil || play.Media.VideoCodec != "H264" {
		t.Fatalf("播放信息应包含 media，实际 %+v", play.Media)
	}
}

// H.265 的编码信息要原样透出，前端据此提示"浏览器放不了"。
func TestPlayReportsHEVC(t *testing.T) {
	stub := srsStreamsStub(t, "HEVC")
	defer stub.Close()

	srv, _ := newFixture(t, func(cfg *config.Config) {
		cfg.SRS.StreamsAPIURL = stub.URL
	})
	postCallback(t, srv, testSecret, publishBody("alice", "爱丽丝的直播间", aliceToken, "c1"))

	play := getJSON[playPayload](t, srv, "/api/rooms/alice/play")
	if play.Media == nil || play.Media.VideoCodec != "HEVC" {
		t.Fatalf("应透出 HEVC，实际 %+v", play.Media)
	}
}

// SRS 查不到（或没配）时，播放接口照常工作，只是不带 media。
func TestPlayWithoutMediaInspector(t *testing.T) {
	srv, _ := newFixture(t, nil)
	postCallback(t, srv, testSecret, publishBody("alice", "爱丽丝的直播间", aliceToken, "c1"))

	play := getJSON[playPayload](t, srv, "/api/rooms/alice/play")
	if play.Media != nil {
		t.Fatalf("未配置 streams 接口时不应有 media，实际 %+v", play.Media)
	}
	if play.State != hub.StateLive {
		t.Fatalf("播放接口不应因为缺少 media 而失败，实际状态 %s", play.State)
	}
}
