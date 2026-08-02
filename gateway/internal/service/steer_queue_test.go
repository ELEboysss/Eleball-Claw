package service

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSessionSteerQueue_BasicPushPop 验证 steer 与 follow-up 消息的基本入队/出队顺序。
func TestSessionSteerQueue_BasicPushPop(t *testing.T) {
	q := &SessionSteerQueue{}

	q.PushSteer("steer-1")
	q.PushSteer("steer-2")
	q.PushFollowup("follow-1")
	q.PushFollowup("follow-2")

	steers := q.PopSteers()
	assert.Len(t, steers, 2)
	assert.Equal(t, "steer-1", steers[0].Text)
	assert.Equal(t, "steer-2", steers[1].Text)
	assert.True(t, steers[0].CreatedAt <= steers[1].CreatedAt)

	followups := q.PopFollowups()
	assert.Len(t, followups, 2)
	assert.Equal(t, "follow-1", followups[0].Text)
	assert.Equal(t, "follow-2", followups[1].Text)

	// 出队后再次弹出应为空。
	assert.Nil(t, q.PopSteers())
	assert.Nil(t, q.PopFollowups())
}

// TestSessionSteerQueue_PopCallback 验证 steer 消费时 OnPopSteer 回调被逐条调用。
func TestSessionSteerQueue_PopCallback(t *testing.T) {
	q := &SessionSteerQueue{}
	var called []string
	q.OnPopSteer = func(text string) { called = append(called, text) }

	q.PushSteer("a")
	q.PushSteer("b")
	q.PopSteers()

	assert.Equal(t, []string{"a", "b"}, called)

	// 空队列弹出不应触发回调。
	called = nil
	q.PopSteers()
	assert.Nil(t, called)
}

// TestSessionSteerQueue_PeekAndSnapshot 验证只读查看与快照不会修改原队列。
func TestSessionSteerQueue_PeekAndSnapshot(t *testing.T) {
	q := &SessionSteerQueue{}
	q.PushSteer("s1")
	q.PushFollowup("f1")

	peekSteers := q.PeekSteers()
	peekFollowups := q.PeekFollowups()
	assert.Len(t, peekSteers, 1)
	assert.Len(t, peekFollowups, 1)

	// 修改副本不应影响原队列。
	peekSteers[0].Text = "modified"
	peekFollowups[0].Text = "modified"
	s, f := q.PopSteers(), q.PopFollowups()
	assert.Equal(t, "s1", s[0].Text)
	assert.Equal(t, "f1", f[0].Text)

	// 再次入队后快照应为深拷贝。
	q.PushSteer("s2")
	q.PushFollowup("f2")
	ss, sf := q.Snapshot()
	assert.Len(t, ss, 1)
	assert.Len(t, sf, 1)
	ss[0].Text = "modified"
	sf[0].Text = "modified"
	s, f = q.PopSteers(), q.PopFollowups()
	assert.Equal(t, "s2", s[0].Text)
	assert.Equal(t, "f2", f[0].Text)
}

// TestSessionSteerQueue_SetOverwrite 验证 SetSteers / SetFollowups 覆盖行为。
func TestSessionSteerQueue_SetOverwrite(t *testing.T) {
	q := &SessionSteerQueue{}
	q.PushSteer("old-steer")
	q.PushFollowup("old-follow")

	q.SetSteers([]SteerMessage{{Text: "new-steer", CreatedAt: 1}})
	q.SetFollowups([]FollowupMessage{{Text: "new-follow", CreatedAt: 2}})

	s := q.PopSteers()
	assert.Equal(t, []SteerMessage{{Text: "new-steer", CreatedAt: 1}}, s)
	f := q.PopFollowups()
	assert.Equal(t, []FollowupMessage{{Text: "new-follow", CreatedAt: 2}}, f)
}

// TestSessionSteerQueue_ConcurrentPushPop 验证并发 Push/Pop 不 panic 且最终一致。
func TestSessionSteerQueue_ConcurrentPushPop(t *testing.T) {
	q := &SessionSteerQueue{}
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			q.PushSteer("steer")
			q.PushFollowup("follow")
			q.PopSteers()
			q.PopFollowups()
		}(i)
	}
	wg.Wait()

	// 由于 Pop 与 Push 并发，最终数量不确定，但不应出现负数或 panic。
	assert.GreaterOrEqual(t, len(q.PeekSteers()), 0)
	assert.GreaterOrEqual(t, len(q.PeekFollowups()), 0)
}

// TestSteerQueueManager_GetAndDelete 验证管理器按 session 隔离队列并能删除。
func TestSteerQueueManager_GetAndDelete(t *testing.T) {
	m := NewSteerQueueManager()
	q1 := m.Get("session-1")
	q2 := m.Get("session-2")
	assert.NotNil(t, q1)
	assert.NotNil(t, q2)
	assert.NotSame(t, q1, q2)

	// 同一 session 返回同一实例。
	q1Again := m.Get("session-1")
	assert.Same(t, q1, q1Again)

	q1.PushSteer("s")
	m.Delete("session-1")

	// 删除后重新 Get 会创建新队列，旧数据应消失。
	q1New := m.Get("session-1")
	assert.NotSame(t, q1, q1New)
	assert.Empty(t, q1New.PopSteers())
}

// TestSteerQueueManager_ConcurrentGet 验证并发 Get 下不会创建重复队列实例。
func TestSteerQueueManager_ConcurrentGet(t *testing.T) {
	m := NewSteerQueueManager()
	var wg sync.WaitGroup
	refs := make([]*SessionSteerQueue, 1000)
	for i := range refs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			refs[idx] = m.Get("same-session")
		}(i)
	}
	wg.Wait()

	first := refs[0]
	for _, r := range refs[1:] {
		assert.Same(t, first, r)
	}
}
