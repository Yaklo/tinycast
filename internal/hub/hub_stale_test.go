package hub

import (
	"testing"
	"time"
)

// 断流后 1 秒内重连：旧会话只是短暂没被确认，仍在失效窗口内，必须继续拒绝。
func TestBindRejectsReconnectWithinStaleWindow(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")
	h.ConfirmLive("alex", nil)

	clock.advance(time.Second) // 默认窗口 3 秒
	res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-b"})
	if res.Allowed || res.Reason != ReasonAlreadyPublishing {
		t.Fatalf("窗口内仍应拒绝并发推流，实际 allowed=%v reason=%s", res.Allowed, res.Reason)
	}
}

// 旧会话超过失效窗口一直没有被确认（巡检还没来得及标记断流，或控制接口不可用）
// → 允许新会话接管，否则重连会被永久拒绝。
func TestBindAllowsReconnectAfterStaleWindow(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")
	h.ConfirmLive("alex", nil)

	clock.advance(4 * time.Second) // > 默认窗口 3 秒
	res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-b"})
	if !res.Allowed {
		t.Fatalf("旧会话已失效时应允许接管，实际 %s", res.Reason)
	}

	// 接管后新会话是新鲜的：旧会话号再回来照样要被拒绝。
	res = h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-a"})
	if res.Allowed || res.Reason != ReasonAlreadyPublishing {
		t.Fatalf("接管后旧会话不应能再来，实际 allowed=%v reason=%s", res.Allowed, res.Reason)
	}
}

// 巡检每个周期都在确认时，旧会话永远不算失效：同身份不能双推的约束不受影响。
func TestBindNeverAllowsReplacementWhileConfirmed(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")

	for i := 0; i < 20; i++ {
		h.ConfirmLive("alex", nil)
		clock.advance(time.Second)
		res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-b"})
		if res.Allowed {
			t.Fatalf("第 %d 次：正在推流的身份不应被顶掉", i+1)
		}
	}
}

// 伪造请求（没有会话 ID）即使旧会话已过期也不能接管：空 id 只可能来自伪造，
// 放行等于把"抢占身份"的口子重新打开。
func TestBindRejectsStaleTakeoverWithoutSessionID(t *testing.T) {
	h, clock := newTestHub()
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")
	h.ConfirmLive("alex", nil)

	clock.advance(10 * time.Second)
	res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken})
	if res.Allowed || res.Reason != ReasonAlreadyPublishing {
		t.Fatalf("空会话 ID 不应能接管旧会话，实际 allowed=%v reason=%s", res.Allowed, res.Reason)
	}
}

// monitor 会按自己的巡检间隔设置窗口；非正值退回默认值。
func TestSetStalePublisherTimeout(t *testing.T) {
	h, clock := newTestHub()
	h.SetStalePublisherTimeout(0)
	h.mu.RLock()
	got := h.staleAfter
	h.mu.RUnlock()
	if got != defaultStalePublisherTimeout {
		t.Fatalf("非正值应退回默认窗口 %s，实际 %s", defaultStalePublisherTimeout, got)
	}

	h.SetStalePublisherTimeout(200 * time.Millisecond)
	mustBind(t, h, "alex", "测试直播", testToken, "session-a")
	h.ConfirmLive("alex", nil)

	clock.advance(100 * time.Millisecond)
	if res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-b"}); res.Allowed {
		t.Fatal("自定义窗口内不应放行")
	}
	clock.advance(200 * time.Millisecond)
	if res := h.Bind(BindInput{Username: "alex", RoomName: "测试直播", Token: testToken, SessionID: "session-b"}); !res.Allowed {
		t.Fatalf("超过自定义窗口应放行，实际 %s", res.Reason)
	}
}
