package service

import (
	"sync"
	"time"
)

// SteerMessage C6：用户在 Agent 执行期间中途注入的 steer 消息，
// 在当前轮次工具调用完成后、下一轮 LLM 调用前作为 user message 注入。
type SteerMessage struct {
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

// FollowupMessage C6：用户在 Agent 执行期间或回答后排队的 follow-up 消息，
// 当前 Agent 回合结束后自动作为新的用户输入继续执行。
type FollowupMessage struct {
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

// SessionSteerQueue 单个 Agent Session 的 steer / follow-up 内存队列。
// 与 AgentService 的生命周期绑定，execute 结束后可持久化到数据库（云端）或随进程消失（claw）。
type SessionSteerQueue struct {
	mu        sync.Mutex
	steers    []SteerMessage
	followups []FollowupMessage
	// OnPopSteer steer 消息被消费时回调，供 AgentService 下发 SSE steer_accepted 事件。
	OnPopSteer func(text string)
}

// PushSteer 添加一条 steer 消息到队列尾部。
func (q *SessionSteerQueue) PushSteer(text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.steers = append(q.steers, SteerMessage{Text: text, CreatedAt: time.Now().UnixMilli()})
}

// PopSteers 取出并清空当前 steer 队列；若设置了 OnPopSteer 则每条消费时回调。
func (q *SessionSteerQueue) PopSteers() []SteerMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.steers) == 0 {
		return nil
	}
	out := q.steers
	q.steers = nil
	if q.OnPopSteer != nil {
		for _, sm := range out {
			q.OnPopSteer(sm.Text)
		}
	}
	return out
}

// PeekSteers 只读查看 steer 队列（UI 展示用）。
func (q *SessionSteerQueue) PeekSteers() []SteerMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]SteerMessage, len(q.steers))
	copy(out, q.steers)
	return out
}

// PushFollowup 添加一条 follow-up 消息到队列尾部。
func (q *SessionSteerQueue) PushFollowup(text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.followups = append(q.followups, FollowupMessage{Text: text, CreatedAt: time.Now().UnixMilli()})
}

// PopFollowups 取出并清空当前 follow-up 队列。
func (q *SessionSteerQueue) PopFollowups() []FollowupMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.followups) == 0 {
		return nil
	}
	out := q.followups
	q.followups = nil
	return out
}

// PeekFollowups 只读查看 follow-up 队列（UI 展示用）。
func (q *SessionSteerQueue) PeekFollowups() []FollowupMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]FollowupMessage, len(q.followups))
	copy(out, q.followups)
	return out
}

// Snapshot 返回当前队列的只读副本，供前端轮询或持久化使用。
func (q *SessionSteerQueue) Snapshot() ([]SteerMessage, []FollowupMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	steers := make([]SteerMessage, len(q.steers))
	copy(steers, q.steers)
	followups := make([]FollowupMessage, len(q.followups))
	copy(followups, q.followups)
	return steers, followups
}

// SetSteers 覆盖 steer 队列（用于云端从数据库加载后回填）。
func (q *SessionSteerQueue) SetSteers(items []SteerMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.steers = items
}

// SetFollowups 覆盖 follow-up 队列（用于云端从数据库加载后回填）。
func (q *SessionSteerQueue) SetFollowups(items []FollowupMessage) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.followups = items
}

// SteerQueueManager 管理所有运行中 Session 的 steer/follow-up 内存队列。
// 每个 execute 通过 Get 获取（或创建）自己 session 的队列；steer/followup HTTP 接口通过同一份 manager 写入。
type SteerQueueManager struct {
	mu     sync.RWMutex
	queues map[string]*SessionSteerQueue
}

// NewSteerQueueManager 创建队列管理器。
func NewSteerQueueManager() *SteerQueueManager {
	return &SteerQueueManager{queues: make(map[string]*SessionSteerQueue)}
}

// Get 获取指定 session 的队列，不存在时自动创建。
func (m *SteerQueueManager) Get(sessionID string) *SessionSteerQueue {
	m.mu.RLock()
	q, ok := m.queues[sessionID]
	m.mu.RUnlock()
	if ok {
		return q
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if q, ok := m.queues[sessionID]; ok {
		return q
	}
	q = &SessionSteerQueue{}
	m.queues[sessionID] = q
	return q
}

// Delete 移除指定 session 的内存队列（execute 结束时调用，防止泄漏）。
func (m *SteerQueueManager) Delete(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.queues, sessionID)
}
