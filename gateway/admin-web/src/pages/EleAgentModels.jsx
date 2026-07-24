import { useEffect, useState } from 'react'
import { eleAgentModelApi } from '../api/client'

/**
 * claw 模型配置（只读列表）。
 * 展示本地可用的 Ele Agent 模型配置（非云端获取）。
 * 本地不计费，调用价格字段不展示；CRUD 在云端 admin-web。
 * 如需接入 Eleball Agent，BaseURL 指向 https://api.eleball.cn/v1，选 OpenAI 兼容协议。
 * 见 docs/marketing/claw-implementation-plan.md §D.1。
 */
export default function EleAgentModels() {
  const [items, setItems] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const pageSize = 20

  useEffect(() => {
    setLoading(true)
    setError('')
    eleAgentModelApi.list(page, pageSize)
      .then((data) => {
        // 后端可能返回 {items,total} 或数组
        const list = data?.items || data || []
        setItems(list)
        setTotal(data?.total ?? list.length)
        // 删除等操作后当前页可能越界，回退到末页
        const totalPages = Math.max(1, Math.ceil((data?.total ?? list.length) / pageSize))
        if (page > totalPages) {
          setPage(totalPages)
        }
      })
      .catch((err) => setError(typeof err === 'string' ? err : (err?.message || '加载失败')))
      .finally(() => setLoading(false))
  }, [page])

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">模型配置</h1>
        <p className="text-sm text-eleball-text-secondary mt-1">
          本地可用的 Ele Agent 模型配置（本地不计费，调用价格由云端账户处理）
        </p>
      </div>

      {/* 接入说明 */}
      <div className="card p-4 border border-eleball-primary/20 bg-eleball-primary-light/30">
        <p className="text-sm text-eleball-text">
          如需接入 Eleball Agent，请修改 Base URL 为{' '}
          <code className="text-xs bg-white px-1.5 py-0.5 rounded">https://api.eleball.cn/v1</code>
          ，并选择 OpenAI 兼容协议。模型配置的增删改请在云端管理后台进行。
        </p>
      </div>

      {error && <div className="p-3 rounded-xl bg-red-50 text-red-600 text-sm">{error}</div>}

      <div className="card p-6">
        <h2 className="text-lg font-semibold mb-4">模型列表</h2>
        {loading ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">加载中…</div>
        ) : items.length === 0 ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">
            暂无模型配置。开发模式下 claw 会预置默认模型。
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left">
              <thead className="text-xs text-eleball-text-secondary border-b border-eleball-outline">
                <tr>
                  <th className="px-3 py-2 font-medium">模型</th>
                  <th className="px-3 py-2 font-medium">协议</th>
                  <th className="px-3 py-2 font-medium">支持对话</th>
                  <th className="px-3 py-2 font-medium">状态</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-eleball-outline-variant">
                {items.map((m) => (
                  <tr key={m.id || m.model}>
                    <td className="px-3 py-2.5">
                      <div className="font-medium">{m.display_name || m.name || m.model}</div>
                      <div className="text-xs text-eleball-text-secondary font-mono">{m.model || m.id}</div>
                    </td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">{m.protocol || '-'}</td>
                    <td className="px-3 py-2.5">
                      {m.supports_chat ? (
                        <span className="text-xs px-2 py-0.5 rounded bg-emerald-50 text-emerald-600">支持</span>
                      ) : (
                        <span className="text-xs px-2 py-0.5 rounded bg-gray-100 text-gray-500">不支持</span>
                      )}
                    </td>
                    <td className="px-3 py-2.5">
                      <span className={`text-xs px-2 py-0.5 rounded ${m.enabled ? 'bg-emerald-50 text-emerald-600' : 'bg-gray-100 text-gray-500'}`}>
                        {m.enabled ? '启用' : '禁用'}
                      </span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* 分页 */}
        {!loading && total > 0 && (
          <div className="flex items-center justify-between mt-4">
            <span className="text-sm text-eleball-text-secondary">
              共 {total} 条记录
            </span>
            <div className="flex gap-2">
              <button
                onClick={() => setPage(p => Math.max(1, p - 1))}
                disabled={page === 1}
                className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-40"
              >
                上一页
              </button>
              <span className="px-3 py-1.5 rounded-lg bg-eleball-primary text-white text-sm font-medium">
                {page} / {totalPages}
              </span>
              <button
                onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                disabled={page >= totalPages}
                className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-40"
              >
                下一页
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
