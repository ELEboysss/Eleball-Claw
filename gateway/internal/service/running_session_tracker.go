package service

import (
	"sort"
	"sync"
)

// RunningSessionTracker 维护每个用户当前正在运行的 Agent Session 集合，
// 并在状态变化时通过关闭 channel 向订阅者广播信号（订阅者随后读取最新集合）。
type RunningSessionTracker struct {
	mu    sync.RWMutex
	users map[string]map[string]struct{} // userID -> set(sessionID)
	subs  map[string]map[chan struct{}]struct{} // userID -> set(subscription channel)
}

// newRunningSessionTracker 创建运行中 Session 跟踪器。
func newRunningSessionTracker() *RunningSessionTracker {
	return &RunningSessionTracker{
		users: make(map[string]map[string]struct{}),
		subs:  make(map[string]map[chan struct{}]struct{}),
	}
}

// MarkRunning 标记指定 Session 为运行中。
func (t *RunningSessionTracker) MarkRunning(userID, sessionID string) {
	if t == nil || userID == "" || sessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.users[userID] == nil {
		t.users[userID] = make(map[string]struct{})
	}
	t.users[userID][sessionID] = struct{}{}
	t.broadcastLocked(userID)
}

// MarkDone 标记指定 Session 已结束（ succeeded / failed 等），从运行集合移除。
func (t *RunningSessionTracker) MarkDone(userID, sessionID string) {
	if t == nil || userID == "" || sessionID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.users[userID] != nil {
		delete(t.users[userID], sessionID)
		if len(t.users[userID]) == 0 {
			delete(t.users, userID)
		}
	}
	t.broadcastLocked(userID)
}

// GetRunningIDs 返回指定用户当前所有运行中 Session ID（按字典序排序，便于前端稳定对比）。
func (t *RunningSessionTracker) GetRunningIDs(userID string) []string {
	if t == nil || userID == "" {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	set := t.users[userID]
	if len(set) == 0 {
		return []string{}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Subscribe 订阅指定用户的运行 Session 变化通知。
// 返回的 channel 在状态变化时会被关闭；订阅者收到信号后应调用 GetRunningIDs 读取最新集合，
// 并重新 Subscribe 以接收下一次变更（单次关闭语义，避免已关闭 channel 复用）。
func (t *RunningSessionTracker) Subscribe(userID string) chan struct{} {
	if t == nil || userID == "" {
		return nil
	}
	ch := make(chan struct{})
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.subs[userID] == nil {
		t.subs[userID] = make(map[chan struct{}]struct{})
	}
	t.subs[userID][ch] = struct{}{}
	return ch
}

// Unsubscribe 取消订阅，释放 channel。
func (t *RunningSessionTracker) Unsubscribe(userID string, ch chan struct{}) {
	if t == nil || userID == "" || ch == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.subs[userID] != nil {
		delete(t.subs[userID], ch)
		if len(t.subs[userID]) == 0 {
			delete(t.subs, userID)
		}
	}
}

// broadcastLocked 向指定用户的所有订阅者发送信号（关闭其 channel），并清空该用户的订阅列表。
// 调用方必须持有写锁；订阅者醒来后需重新 Subscribe。
func (t *RunningSessionTracker) broadcastLocked(userID string) {
	subs := t.subs[userID]
	if len(subs) == 0 {
		return
	}
	for ch := range subs {
		close(ch)
	}
	delete(t.subs, userID)
}
