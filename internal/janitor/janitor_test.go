package janitor

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"tinycast/internal/hub"
	"tinycast/internal/srsapi"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newHub() (*hub.Hub, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	return hub.New(hub.Options{Grace: 5 * time.Minute, Now: clock.Now}), clock
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func publish(t *testing.T, h *hub.Hub, username, clientID string) {
	t.Helper()
	res := h.Publish(hub.PublishInput{
		Username: username,
		RoomName: "房间",
		Token:    "AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd",
		ClientID: clientID,
	})
	if !res.Allowed {
		t.Fatalf("推流应被放行: %s", res.Reason)
	}
}

// 超期的 GRACE 房间应在每轮任务中被释放。
func TestTickReleasesExpiredRooms(t *testing.T) {
	h, clock := newHub()
	publish(t, h, "alice", "c1")
	h.Unpublish("alice", "c1")

	runner := New(Options{Hub: h, Log: discardLogger(), Interval: time.Second, App: "live"})

	runner.tick(context.Background())
	if _, ok := h.Room("alice"); !ok {
		t.Fatal("未到期的房间不应被释放")
	}

	clock.Advance(6 * time.Minute)
	runner.tick(context.Background())
	if _, ok := h.Room("alice"); ok {
		t.Fatal("到期后房间应被释放")
	}
}

// SRS 中已经不存在的推流连接会被判定为断开（回调丢失兜底）。
func TestTickReconcilesAgainstSRS(t *testing.T) {
	h, clock := newHub()
	publish(t, h, "alice", "c1")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "clients": []any{}})
	}))
	defer ts.Close()

	runner := New(Options{
		Hub:       h,
		Log:       discardLogger(),
		Interval:  time.Second,
		App:       "live",
		Inspector: srsapi.New(ts.URL+"/api/v1/clients/", "", ""),
	})

	// 会话太年轻：不做判断。
	runner.tick(context.Background())
	if view, _ := h.Room("alice"); view.State != hub.StateLive {
		t.Fatalf("年轻会话不应被核对，实际 %s", view.State)
	}

	clock.Advance(30 * time.Second)
	runner.tick(context.Background())
	if view, _ := h.Room("alice"); view.State != hub.StateGrace {
		t.Fatalf("SRS 中已不存在的推流应进入 GRACE，实际 %s", view.State)
	}
}

// SRS 不可达时不能改动任何内存状态。
func TestTickKeepsStateWhenSRSUnreachable(t *testing.T) {
	h, clock := newHub()
	publish(t, h, "alice", "c1")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	runner := New(Options{
		Hub:       h,
		Log:       discardLogger(),
		Interval:  time.Second,
		App:       "live",
		Inspector: srsapi.New(ts.URL+"/api/v1/clients/", "", ""),
	})

	clock.Advance(time.Minute)
	runner.tick(context.Background())
	if view, _ := h.Room("alice"); view.State != hub.StateLive {
		t.Fatalf("查询失败时应保持 LIVE，实际 %s", view.State)
	}
}

// 只统计本 app 的客户端：其他 app 的同名客户端不算"存活"。
func TestTickOnlyCountsOwnAppClients(t *testing.T) {
	h, clock := newHub()
	publish(t, h, "alice", "c1")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"clients": []map[string]any{
				{"id": "x1", "app": "other", "stream": "alice", "publish": true},
			},
		})
	}))
	defer ts.Close()

	runner := New(Options{
		Hub:       h,
		Log:       discardLogger(),
		Interval:  time.Second,
		App:       "live",
		Inspector: srsapi.New(ts.URL+"/api/v1/clients/", "", ""),
	})

	clock.Advance(time.Minute)
	runner.tick(context.Background())
	if view, _ := h.Room("alice"); view.State != hub.StateGrace {
		t.Fatalf("其他 app 的客户端不应被当作本应用连接，实际 %s", view.State)
	}
}

// Run 在 ctx 取消后应退出。
func TestRunStopsOnContextCancel(t *testing.T) {
	h, _ := newHub()
	runner := New(Options{Hub: h, Log: discardLogger(), Interval: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 应在 ctx 取消后退出")
	}
}
