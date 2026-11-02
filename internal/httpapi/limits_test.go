package httpapi

import (
	"testing"
	"time"
)

func TestFixedWindowLimiter(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter := newFixedWindowLimiter(2, 10*time.Second)
	limiter.now = func() time.Time { return now }

	if !limiter.allow("127.0.0.1") {
		t.Fatal("第一次请求应放行")
	}
	if !limiter.allow("127.0.0.1") {
		t.Fatal("窗口内第二次请求应放行")
	}
	if limiter.allow("127.0.0.1") {
		t.Fatal("超过上限应被拒绝")
	}
	if !limiter.allow("127.0.0.2") {
		t.Fatal("不同来源不应共享配额")
	}

	now = now.Add(10 * time.Second)
	if !limiter.allow("127.0.0.1") {
		t.Fatal("新窗口应重新放行")
	}
}

func TestFixedWindowLimiterDisabled(t *testing.T) {
	var limiter *fixedWindowLimiter
	if !limiter.allow("127.0.0.1") {
		t.Fatal("nil 限流器不应拦截请求")
	}
}
