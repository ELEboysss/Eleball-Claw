import { useState, useEffect } from 'react'
import { Loader2 } from 'lucide-react'
import { agentApi } from '../api/client'
import ConfirmDialog from './ConfirmDialog'

// AgentSessionList 展示当前用户的历史 Agent Session 列表
export default function AgentSessionList({ onSelect, onRefresh, runningSessionIds = new Set() }) {
  const [sessions, setSessions] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  // AR-05-O5：确认弹窗状态
  const [confirmState, setConfirmState] = useState({ open: false })

  const fetchSessions = async () => {
    setLoading(true)
    setError('')
    try {
      const data = await agentApi.listSessions(1, 50)
      setSessions(data?.items || [])
    } catch (err) {
      setError(err.message || '网络错误')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchSessions()
  }, [onRefresh])

  const askConfirm = (cfg) => new Promise((resolve) => {
    setConfirmState({
      open: true,
      ...cfg,
      onConfirm: () => { setConfirmState({ open: false }); resolve(true) },
      onCancel: () => { setConfirmState({ open: false }); resolve(false) }
    })
  })

  const handleDelete = async (e, id) => {
    e.stopPropagation()
    // AR-05-O5：ConfirmDialog 替代 confirm()
    const ok = await askConfirm({
      title: '删除 Session',
      message: '确定删除该 Agent Session 及其产物吗？'
    })
    if (!ok) return
    try {
      await agentApi.deleteSession(id)
      setSessions((prev) => prev.filter((s) => s.id !== id))
    } catch (err) {
      // AR-05-O5：alert 改为 ConfirmDialog（danger=false 信息态）
      await askConfirm({
        title: '删除失败',
        message: err.message || '删除失败',
        danger: false,
        confirmText: '好的',
        cancelText: '关闭'
      })
    }
  }

  const handleDeleteAll = async () => {
    const ok = await askConfirm({
      title: '全部删除',
      message: '确定删除所有 Agent 关键节点及其产物吗？'
    })
    if (!ok) return
    try {
      await agentApi.deleteAllSessions()
      fetchSessions()
    } catch (err) {
      setError(err.message || '删除失败')
    }
  }

  const statusClass = (status) => {
    switch (status) {
      case 'succeeded': return 'text-eleball-success'
      case 'failed': return 'text-eleball-error'
      case 'running': return 'text-eleball-primary'
      default: return 'text-eleball-text-tertiary'
    }
  }

  if (loading && sessions.length === 0) {
    return <div className="text-sm text-eleball-text-tertiary py-2">加载中...</div>
  }

  if (error) {
    return <div className="text-sm text-eleball-error py-2">{error}</div>
  }

  if (sessions.length === 0) {
    return <div className="text-sm text-eleball-text-tertiary py-2">暂无 Agent Session</div>
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-eleball-text-secondary">Agent 关键节点</h3>
        <div className="flex items-center gap-2">
          <button
            onClick={fetchSessions}
            className="text-xs text-eleball-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-eleball-primary rounded"
            aria-label="刷新 Agent Session 列表"
          >
            刷新
          </button>
          <button
            onClick={handleDeleteAll}
            className="text-xs text-eleball-error hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-eleball-primary rounded"
            aria-label="删除全部 Agent Session"
          >
            全部删除
          </button>
        </div>
      </div>
      <div className="space-y-1 pr-1">
        {sessions.map((session) => (
          // AR-05-O2：div onClick 改原生 button，键盘可达 + focus-visible
          <button
            key={session.id}
            type="button"
            onClick={() => onSelect?.(session)}
            className="group w-full text-left flex items-center justify-between p-2 rounded-md bg-eleball-surface hover:bg-eleball-primary/10 cursor-pointer border border-transparent hover:border-eleball-primary/30 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-eleball-primary"
            aria-label={`查看 Session ${session.title || '未命名'}`}
          >
            <div className="min-w-0 flex-1">
              <div className="text-sm text-eleball-text truncate">
                {session.title || '未命名 Session'}
              </div>
              <div className="text-xs text-eleball-text-tertiary flex items-center gap-2 mt-0.5">
                {runningSessionIds.has(session.id) ? (
                  <span className="inline-flex items-center gap-1 text-eleball-primary">
                    <Loader2 className="w-3 h-3 animate-spin" aria-hidden="true" />
                    运行中
                  </span>
                ) : (
                  <span className={statusClass(session.status)}>{session.status}</span>
                )}
                <span>{new Date(session.created_at * 1000).toLocaleString()}</span>
              </div>
            </div>
            <span
              role="button"
              tabIndex={-1}
              onClick={(e) => handleDelete(e, session.id)}
              className="ml-2 text-xs text-eleball-error opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100 hover:underline focus-visible:opacity-100"
              aria-label={`删除 Session ${session.title || '未命名'}`}
            >
              删除
            </span>
          </button>
        ))}
      </div>
      {/* AR-05-O5：确认弹窗 */}
      <ConfirmDialog
        open={confirmState.open}
        title={confirmState.title}
        message={confirmState.message}
        danger={confirmState.danger !== false}
        confirmText={confirmState.confirmText}
        cancelText={confirmState.cancelText}
        onConfirm={confirmState.onConfirm}
        onCancel={confirmState.onCancel}
      />
    </div>
  )
}
