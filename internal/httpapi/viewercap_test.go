package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// 只对新的 viewerId 计数：重复心跳命中同一个 ID，不占新配额。
func TestViewerCapLimitsDistinctViewers(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newViewerCap(4, time.Minute)
	c.now = func() time.Time { return now }

	for i := 0; i < 4; i++ {
		allowed, added := c.allow("alex|203.0.113.7", fmt.Sprintf("viewer-%d", i))
		if !allowed || !added {
			t.Fatalf("第 %d 个观众应放行且计为新增，实际 allowed=%v added=%v", i+1, allowed, added)
		}
	}
	if allowed, _ := c.allow("alex|203.0.113.7", "viewer-over"); allowed {
		t.Fatal("超过上限的新 viewerId 应被拒绝")
	}
	// 已存在的 viewerId 即使填满也要继续放行，否则正常观众会被自己的心跳踢掉。
	if allowed, added := c.allow("alex|203.0.113.7", "viewer-1"); !allowed || added {
		t.Fatalf("重复心跳应放行且不算新增，实际 allowed=%v added=%v", allowed, added)
	}
	// 不同的房间、不同的客户端互不影响。
	if allowed, _ := c.allow("bob|203.0.113.7", "viewer-over"); !allowed {
		t.Fatal("其它房间不应受影响")
	}
	if allowed, _ := c.allow("alex|203.0.113.8", "viewer-over"); !allowed {
		t.Fatal("其它客户端不应受影响")
	}

	// 条目过期后配额要还回来，观众重新进入不会被误判成超限。
	now = now.Add(2 * time.Minute)
	if allowed, added := c.allow("alex|203.0.113.7", "viewer-new"); !allowed || !added {
		t.Fatalf("过期后应重新可加入，实际 allowed=%v added=%v", allowed, added)
	}
}

// 心跳最终没被 hub 采纳时用 forget 撤回，别让失败请求白占配额。
func TestViewerCapForgetReleasesQuota(t *testing.T) {
	c := newViewerCap(1, time.Minute)
	if allowed, _ := c.allow("k", "v1"); !allowed {
		t.Fatal("首个 viewerId 应放行")
	}
	if allowed, _ := c.allow("k", "v2"); allowed {
		t.Fatal("上限为 1 时第二个 viewerId 应被拒绝")
	}
	c.forget("k", "v1")
	if allowed, _ := c.allow("k", "v2"); !allowed {
		t.Fatal("撤回后应可以加入")
	}
	c.forget("k", "v2")
	if len(c.groups) != 0 {
		t.Fatalf("空分组应被删除，实际 %d 个", len(c.groups))
	}
	// nil 与 limit<=0 都表示不限制，不能 panic。
	var nilCap *viewerCap
	if allowed, _ := nilCap.allow("k", "v1"); !allowed {
		t.Fatal("nil 限制器应恒放行")
	}
	nilCap.forget("k", "v1")
}

// 长时间运行后 key 数量不能无界增长：整组空闲就要清掉。
func TestViewerCapDropsIdleGroups(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newViewerCap(16, time.Minute)
	c.now = func() time.Time { return now }

	c.allow("alex|A", "v1")
	c.allow("alex|B", "v2")
	if len(c.groups) != 2 {
		t.Fatalf("应有 2 个分组，实际 %d 个", len(c.groups))
	}

	now = now.Add(2 * time.Minute)
	c.allow("bob|C", "v3")
	if len(c.groups) != 1 {
		t.Fatalf("空闲分组应被清理，实际 %d 个", len(c.groups))
	}
	if _, ok := c.groups["bob|C"]; !ok {
		t.Fatal("刚写入的分组不应被清理")
	}
}
