import { useEffect, useState } from 'react'
import { dashboardApi } from '../api/client'

// capabilities 后端以 JSON 字符串存储/返回（如 "[\"scrape\",\"crawl\"]")，兼容数组与字符串两种形态
function parseCapabilities(raw) {
  if (Array.isArray(raw)) return raw
  if (typeof raw === 'string' && raw) {
    try {
      const v = JSON.parse(raw)
      return Array.isArray(v) ? v : []
    } catch {
      return []
    }
  }
  return []
}

// claw 本地控制台（替代云端 Dashboard）。
// 不展示 DAU/总收入/总用户等平台级数据，只展示本地节点信息与本地模块状态。
// 见 docs/marketing/claw-implementation-plan.md §D.2。
export default function Dashboard() {
  const [modules, setModules] = useState([])
  const [stats, setStats] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([
      dashboardApi.getModules().catch(() => null),
      dashboardApi.getStats().catch(() => null),
    ])
      .then(([modData, statsData]) => {
        // 兼容 {items:[...]} / 直接数组两种返回，异常形态兜底为空数组，避免渲染期崩溃白屏
        const list = modData?.items ?? modData
        setModules(Array.isArray(list) ? list : [])
        setStats(statsData)
      })
      .catch((err) => setError(typeof err === 'string' ? err : (err?.message || '加载失败')))
      .finally(() => setLoading(false))
  }, [])

  const onlineCount = modules.filter((m) => m.status === 'online').length
  const totalCapabilities = modules.reduce((acc, m) => acc + parseCapabilities(m.capabilities).length, 0)
  const usage = stats?.usage || {}
  const modelStats = stats?.models || []

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">本地控制台</h1>
        <p className="text-sm text-eleball-text-secondary mt-1">
          claw 本地节点状态与模块运行情况（数据不出本地）
        </p>
      </div>

      {/* 节点信息卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="card p-5">
          <div className="text-xs text-eleball-text-secondary mb-1">节点</div>
          <div className="text-lg font-semibold">Eleball claw</div>
          <div className="text-xs text-eleball-text-secondary mt-1">本地网关运行中</div>
        </div>
        <div className="card p-5">
          <div className="text-xs text-eleball-text-secondary mb-1">本地模块</div>
          <div className="text-lg font-semibold">
            <span className="text-emerald-600">{onlineCount}</span>
            <span className="text-eleball-text-secondary text-sm"> / {modules.length} 在线</span>
          </div>
          <div className="text-xs text-eleball-text-secondary mt-1">{totalCapabilities} 项能力</div>
        </div>
        <div className="card p-5">
          <div className="text-xs text-eleball-text-secondary mb-1">计费</div>
          <div className="text-lg font-semibold text-eleball-primary">本地不计费</div>
          <div className="text-xs text-eleball-text-secondary mt-1">Ele Agent 转发云端计费</div>
        </div>
      </div>

      {/* 本地 token 用量统计（P3 细化，替代云端 DAU/收入） */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <div className="card p-4">
          <div className="text-xs text-eleball-text-secondary mb-1">总调用</div>
          <div className="text-lg font-semibold">{Number(usage.total_calls || 0).toLocaleString()}</div>
        </div>
        <div className="card p-4">
          <div className="text-xs text-eleball-text-secondary mb-1">今日调用</div>
          <div className="text-lg font-semibold text-emerald-600">{Number(usage.today_calls || 0).toLocaleString()}</div>
        </div>
        <div className="card p-4">
          <div className="text-xs text-eleball-text-secondary mb-1">输入 tokens</div>
          <div className="text-lg font-semibold">{Number(usage.total_input_tokens || 0).toLocaleString()}</div>
        </div>
        <div className="card p-4">
          <div className="text-xs text-eleball-text-secondary mb-1">输出 tokens</div>
          <div className="text-lg font-semibold">{Number(usage.total_output_tokens || 0).toLocaleString()}</div>
        </div>
      </div>

      {/* 本地模块列表 */}
      <div className="card p-6">
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold">本地模块状态</h2>
          <span className="text-xs text-eleball-text-secondary">来源：本地扫描 + 已安装</span>
        </div>
        <p className="text-xs text-eleball-text-tertiary mb-3">
          模块是独立进程，claw 只负责探测与调用；离线通常表示模块未启动（可用 marketplace/start.sh 以 docker 拉起，或按各模块 README 部署）。
        </p>

        {loading ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">加载中…</div>
        ) : error ? (
          <div className="p-3 rounded-xl bg-red-50 text-red-600 text-sm">{error}</div>
        ) : modules.length === 0 ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">
            暂无本地模块。claw 启动时会扫描 marketplace/ 预置官方模块。
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left">
              <thead className="text-xs text-eleball-text-secondary border-b border-eleball-outline">
                <tr>
                  <th className="px-3 py-2 font-medium">模块</th>
                  <th className="px-3 py-2 font-medium">传输</th>
                  <th className="px-3 py-2 font-medium">状态</th>
                  <th className="px-3 py-2 font-medium">能力</th>
                  <th className="px-3 py-2 font-medium">版本</th>
                  <th className="px-3 py-2 font-medium">心跳</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-eleball-outline-variant">
                {modules.map((m) => {
                  const caps = parseCapabilities(m.capabilities)
                  return (
                  <tr key={m.module_id}>
                    <td className="px-3 py-2.5">
                      <div className="font-medium">{m.name || m.module_id}</div>
                      <div className="text-xs text-eleball-text-secondary font-mono">{m.module_id}</div>
                    </td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">{m.transport_type || '-'}</td>
                    <td className="px-3 py-2.5">
                      <StatusBadge status={m.status} />
                      {/* 离线时展示探测错误原因（如模块进程未启动），帮助用户自查 */}
                      {m.status !== 'online' && m.error && (
                        <div className="text-[10px] text-eleball-text-tertiary mt-1 max-w-[200px] truncate" title={m.error}>
                          {m.error}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2.5">
                      <div className="flex flex-wrap gap-1">
                        {caps.slice(0, 4).map((c) => (
                          <span key={c} className="text-[10px] px-1.5 py-0.5 rounded bg-gray-100 text-eleball-text-secondary">
                            {c}
                          </span>
                        ))}
                        {caps.length > 4 && (
                          <span className="text-[10px] text-eleball-text-secondary">+{caps.length - 4}</span>
                        )}
                      </div>
                    </td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">{m.version || '-'}</td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">
                      {m.last_heartbeat ? new Date(m.last_heartbeat).toLocaleString('zh-CN') : '-'}
                    </td>
                  </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function StatusBadge({ status }) {
  const map = {
    online: { cls: 'bg-emerald-50 text-emerald-600', label: '在线' },
    offline: { cls: 'bg-gray-100 text-gray-500', label: '离线' },
    disabled: { cls: 'bg-red-50 text-red-600', label: '已禁用' }
  }
  const s = map[status] || { cls: 'bg-gray-100 text-gray-500', label: status || '-' }
  return <span className={`text-xs px-2 py-0.5 rounded ${s.cls}`}>{s.label}</span>
}
