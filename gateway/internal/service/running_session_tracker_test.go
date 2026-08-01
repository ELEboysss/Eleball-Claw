package service

import (
	"testing"
	"time"
)

// TestRunningSessionTracker_MarkRunningAndDone 验证运行中 Session 的增删与查询。
func TestRunningSessionTracker_MarkRunningAndDone(t *testing.T) {
	tr := newRunningSessionTracker()
	userID := "u_001"
	sessionID := "as_001"

	// 初始为空
	ids := tr.GetRunningIDs(userID)
	if len(ids) != 0 {
		t.Fatalf("期望初始为空，得到 %v", ids)
	}

	// 标记运行中
	tr.MarkRunning(userID, sessionID)
	ids = tr.GetRunningIDs(userID)
	if len(ids) != 1 || ids[0] != sessionID {
		t.Fatalf("期望 [%s]，得到 %v", sessionID, ids)
	}

	// 重复标记不重复
	tr.MarkRunning(userID, sessionID)
	ids = tr.GetRunningIDs(userID)
	if len(ids) != 1 {
		t.Fatalf("期望去重后仍为一个，得到 %v", ids)
	}

	// 标记结束
	tr.MarkDone(userID, sessionID)
	ids = tr.GetRunningIDs(userID)
	if len(ids) != 0 {
		t.Fatalf("期望标记结束后为空，得到 %v", ids)
	}
}

// TestRunningSessionTracker_MultiUser 验证不同用户之间的隔离。
func TestRunningSessionTracker_MultiUser(t *testing.T) {
	tr := newRunningSessionTracker()
	tr.MarkRunning("u_a", "as_a1")
	tr.MarkRunning("u_a", "as_a2")
	tr.MarkRunning("u_b", "as_b1")

	aIDs := tr.GetRunningIDs("u_a")
	bIDs := tr.GetRunningIDs("u_b")
	if len(aIDs) != 2 {
		t.Fatalf("期望 u_a 有两个运行中 session，得到 %v", aIDs)
	}
	if len(bIDs) != 1 || bIDs[0] != "as_b1" {
		t.Fatalf("期望 u_b 有 [as_b1]，得到 %v", bIDs)
	}

	// 结束 u_a 的一个 session，不应影响 u_b
	tr.MarkDone("u_a", "as_a1")
	aIDs = tr.GetRunningIDs("u_a")
	bIDs = tr.GetRunningIDs("u_b")
	if len(aIDs) != 1 || aIDs[0] != "as_a2" {
		t.Fatalf("期望 u_a 剩余 [as_a2]，得到 %v", aIDs)
	}
	if len(bIDs) != 1 {
		t.Fatalf("期望 u_b 仍有一个，得到 %v", bIDs)
	}
}

// TestRunningSessionTracker_Subscribe 验证订阅者在状态变化时收到信号。
func TestRunningSessionTracker_Subscribe(t *testing.T) {
	tr := newRunningSessionTracker()
	userID := "u_002"

	ch := tr.Subscribe(userID)
	if ch == nil {
		t.Fatal("Subscribe 返回 nil")
	}

	// 标记运行中应触发广播
	tr.MarkRunning(userID, "as_002")
	select {
	case <-ch:
		// 收到信号
	case <-time.After(time.Second):
		t.Fatal("未收到状态变化信号")
	}

	// 订阅被触发后应重新订阅才能继续接收下一次变更
	ch = tr.Subscribe(userID)
	tr.MarkDone(userID, "as_002")
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("未收到结束信号")
	}
}

// TestRunningSessionTracker_Unsubscribe 验证取消订阅后不再收到信号。
func TestRunningSessionTracker_Unsubscribe(t *testing.T) {
	tr := newRunningSessionTracker()
	userID := "u_003"

	ch := tr.Subscribe(userID)
	tr.Unsubscribe(userID, ch)

	tr.MarkRunning(userID, "as_003")
	select {
	case <-ch:
		t.Fatal("已取消订阅不应再收到信号")
	case <-time.After(100 * time.Millisecond):
		// 符合预期
	}
}

// TestRunningSessionTracker_GetRunningIDsSorted 验证返回的 ID 按字典序排序。
func TestRunningSessionTracker_GetRunningIDsSorted(t *testing.T) {
	tr := newRunningSessionTracker()
	tr.MarkRunning("u_004", "as_z")
	tr.MarkRunning("u_004", "as_a")
	tr.MarkRunning("u_004", "as_m")

	ids := tr.GetRunningIDs("u_004")
	expected := []string{"as_a", "as_m", "as_z"}
	if len(ids) != len(expected) {
		t.Fatalf("期望 %v，得到 %v", expected, ids)
	}
	for i := range expected {
		if ids[i] != expected[i] {
			t.Fatalf("期望 %v，得到 %v", expected, ids)
		}
	}
}
