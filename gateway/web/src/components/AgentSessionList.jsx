import { useState, useEffect } from 'react'
import { agentApi } from '../api/client'

// AgentSessionList 展示当前用户的历史 Agent Session 列表
export default function AgentSessionList({ onSelect, onRefresh }) {
  const [sessions, setSessions] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

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

  const handleDelete = async (e, id) => {
    e.stopPropagation()
    if (!confirm('确定删除该 Agent Session 及其产物吗？')) return
    try {
      await agentApi.deleteSession(id)
      setSessions((prev) => prev.filter((s) => s.id !== id))
    } catch (err) {
      alert(err.message || '删除失败')
    }
  }

  const statusClass = (status) => {
    switch (status) {
      case 'succeeded': return 'text-green-600'
      case 'failed': return 'text-red-600'
      case 'running': return 'text-blue-600'
      default: return 'text-gray-500'
    }
  }

  if (loading && sessions.length === 0) {
    return <div className="text-sm text-gray-500 py-2">加载中...</div>
  }

  if (error) {
    return <div className="text-sm text-red-500 py-2">{error}</div>
  }

  if (sessions.length === 0) {
    return <div className="text-sm text-gray-400 py-2">暂无 Agent Session</div>
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium text-gray-700">Agent 关键节点</h3>
        <div className="flex items-center gap-2">
          <button onClick={fetchSessions} className="text-xs text-blue-600 hover:underline">刷新</button>
          <button
            onClick={async () => {
              if (!confirm('确定删除所有 Agent 关键节点及其产物吗？')) return
              try {
                await agentApi.deleteAllSessions()
                fetchSessions()
              } catch (err) {
                setError(err.message || '删除失败')
              }
            }}
            className="text-xs text-red-500 hover:underline"
          >
            全部删除
          </button>
        </div>
      </div>
      <div className="space-y-1 pr-1">
        {sessions.map((session) => (
          <div
            key={session.id}
            onClick={() => onSelect?.(session)}
            className="group flex items-center justify-between p-2 rounded-md bg-gray-50 hover:bg-blue-50 cursor-pointer border border-transparent hover:border-blue-200 transition-colors"
          >
            <div className="min-w-0 flex-1">
              <div className="text-sm text-gray-800 truncate">
                {session.title || '未命名 Session'}
              </div>
              <div className="text-xs text-gray-500 flex gap-2 mt-0.5">
                <span className={statusClass(session.status)}>{session.status}</span>
                <span>{new Date(session.created_at * 1000).toLocaleString()}</span>
              </div>
            </div>
            <button
              onClick={(e) => handleDelete(e, session.id)}
              className="ml-2 text-xs text-red-500 opacity-0 group-hover:opacity-100 hover:underline"
            >
              删除
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}
