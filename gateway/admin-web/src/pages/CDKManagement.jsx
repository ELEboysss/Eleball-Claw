import { useEffect, useState, useMemo } from 'react'
import { cdkApi } from '../api/client'

// 把无横杠兑换码格式化为 XXXX-XXXX-XXXX-XXXX 展示
function formatCDK(code) {
  if (!code || code.length <= 4) return code
  const parts = []
  for (let i = 0; i < code.length; i += 4) {
    parts.push(code.slice(i, i + 4))
  }
  return parts.join('-')
}

function formatTime(iso) {
  if (!iso) return '-'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '-'
  return d.toLocaleString('zh-CN')
}

export default function CDKManagement() {
  const [items, setItems] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  // 批量生成表单
  const [value, setValue] = useState('')
  const [count, setCount] = useState('')
  const [note, setNote] = useState('')
  const [generating, setGenerating] = useState(false)

  // 筛选
  const [filters, setFilters] = useState({
    status: '',
    value: '',
    search: '',
    page: 1,
    page_size: 20
  })

  const fetchItems = async () => {
    setLoading(true)
    setError('')
    try {
      const params = {}
      if (filters.status) params.status = filters.status
      if (filters.value) params.value = filters.value
      if (filters.search) params.search = filters.search
      params.page = filters.page
      params.page_size = filters.page_size
      const res = await cdkApi.list(params)
      setItems(res?.items || [])
      setTotal(res?.total || 0)
    } catch (err) {
      setError(err?.message || err || '加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchItems()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filters.status, filters.value, filters.page, filters.page_size])

  const handleSearch = (e) => {
    e.preventDefault()
    setFilters((prev) => ({ ...prev, page: 1 }))
    fetchItems()
  }

  const handleGenerate = async (e) => {
    e.preventDefault()
    setError('')
    const val = Number(value)
    const cnt = Number(count)
    if (!val || val <= 0) {
      setError('面值必须大于 0')
      return
    }
    if (!cnt || cnt <= 0 || cnt > 500) {
      setError('数量必须在 1-500 之间')
      return
    }
    setGenerating(true)
    try {
      await cdkApi.batchGenerate(val, cnt, note)
      setValue('')
      setCount('')
      setNote('')
      setFilters((prev) => ({ ...prev, page: 1 }))
      await fetchItems()
    } catch (err) {
      setError(err?.message || err || '生成失败')
    } finally {
      setGenerating(false)
    }
  }

  const handleDelete = async (id) => {
    if (!window.confirm('确定删除该兑换码？')) return
    try {
      await cdkApi.delete(id)
      await fetchItems()
    } catch (err) {
      setError(err?.message || err || '删除失败')
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / filters.page_size))

  const codesText = useMemo(() => items.map((i) => i.code).join('\n'), [items])
  const [copied, setCopied] = useState(false)

  const handleCopyAll = async () => {
    if (!codesText) return
    try {
      await navigator.clipboard.writeText(codesText)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // fallback
      const textarea = document.createElement('textarea')
      textarea.value = codesText
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  const handlePrint = () => {
    window.print()
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">兑换码管理</h1>
          <p className="text-eleball-text-secondary mt-1">批量生成、查看、筛选和删除 CDK 兑换码库存</p>
        </div>
      </div>

      {error && (
        <div className="text-sm text-eleball-error bg-red-50 rounded-xl px-4 py-3">{error}</div>
      )}

      {/* 批量生成 */}
      <div className="card space-y-4">
        <h3 className="text-base font-semibold">批量生成兑换码</h3>
        <form onSubmit={handleGenerate} className="grid grid-cols-1 md:grid-cols-4 gap-4 items-end">
          <div>
            <label className="block text-sm font-medium mb-1.5">面值（弹丸数）</label>
            <input
              type="number"
              min="1"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="如 1000"
              className="input"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">生成数量</label>
            <input
              type="number"
              min="1"
              max="500"
              value={count}
              onChange={(e) => setCount(e.target.value)}
              placeholder="1-500"
              className="input"
              required
            />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">备注（可选）</label>
            <input
              type="text"
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="如 618 活动"
              className="input"
            />
          </div>
          <button type="submit" disabled={generating} className="btn-primary justify-center">
            {generating ? '生成中...' : '批量生成'}
          </button>
        </form>
      </div>

      {/* 筛选 */}
      <div className="card space-y-4">
        <h3 className="text-base font-semibold">快速筛选</h3>
        <form onSubmit={handleSearch} className="grid grid-cols-1 md:grid-cols-4 gap-4 items-end">
          <div>
            <label className="block text-sm font-medium mb-1.5">状态</label>
            <select
              value={filters.status}
              onChange={(e) => setFilters((prev) => ({ ...prev, status: e.target.value, page: 1 }))}
              className="input"
            >
              <option value="">全部</option>
              <option value="unused">未使用</option>
              <option value="used">已使用</option>
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">面值</label>
            <input
              type="number"
              min="0"
              value={filters.value}
              onChange={(e) => setFilters((prev) => ({ ...prev, value: e.target.value, page: 1 }))}
              placeholder="如 1000"
              className="input"
            />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">兑换码搜索</label>
            <input
              type="text"
              value={filters.search}
              onChange={(e) => setFilters((prev) => ({ ...prev, search: e.target.value }))}
              placeholder="输入部分码值"
              className="input"
            />
          </div>
          <button type="submit" className="btn-secondary justify-center">
            搜索
          </button>
        </form>
      </div>

      {/* 批量复制 */}
      <div className="card space-y-4">
        <div className="flex items-center justify-between">
          <h3 className="text-base font-semibold">批量复制兑换码</h3>
          <button
            type="button"
            onClick={handleCopyAll}
            disabled={!codesText}
            className="btn-secondary disabled:opacity-50"
          >
            {copied ? '已复制' : '复制全部'}
          </button>
        </div>
        <textarea
          value={codesText}
          readOnly
          rows={Math.min(10, Math.max(3, items.length))}
          placeholder={items.length === 0 ? '暂无兑换码' : ''}
          className="input w-full font-mono text-sm"
        />
      </div>

      {/* 列表 */}
      <div className="card overflow-hidden print-area">
        <div className="flex items-center justify-between px-4 py-3 border-b border-eleball-outline no-print">
          <h3 className="text-base font-semibold">兑换码列表</h3>
          <button
            type="button"
            onClick={handlePrint}
            disabled={items.length === 0}
            className="btn-secondary disabled:opacity-50"
          >
            打印
          </button>
        </div>
        {loading ? (
          <div className="p-8 text-sm text-eleball-text-secondary">加载中...</div>
        ) : (
          <>
            <table className="w-full text-sm">
              <thead className="bg-gray-50 text-eleball-text-secondary">
                <tr>
                  <th className="text-left px-4 py-3 font-medium">兑换码</th>
                  <th className="text-left px-4 py-3 font-medium">面值</th>
                  <th className="text-left px-4 py-3 font-medium">状态</th>
                  <th className="text-left px-4 py-3 font-medium">使用人</th>
                  <th className="text-left px-4 py-3 font-medium">使用时间</th>
                  <th className="text-left px-4 py-3 font-medium">创建时间</th>
                  <th className="text-left px-4 py-3 font-medium">备注</th>
                  <th className="text-right px-4 py-3 font-medium no-print">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-eleball-outline">
                {items.map((item) => (
                  <tr key={item.id} className="hover:bg-gray-50">
                    <td className="px-4 py-3 font-mono text-eleball-text">
                      {formatCDK(item.code)}
                    </td>
                    <td className="px-4 py-3">{item.value.toLocaleString('zh-CN')} 弹丸</td>
                    <td className="px-4 py-3">
                      <span
                        className={`inline-flex px-2 py-1 rounded-lg text-xs font-medium ${
                          item.used
                            ? 'bg-gray-100 text-gray-600'
                            : 'bg-emerald-50 text-emerald-700'
                        }`}
                      >
                        {item.used ? '已使用' : '未使用'}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-eleball-text-secondary">{item.used_by || '-'}</td>
                    <td className="px-4 py-3 text-eleball-text-secondary">{formatTime(item.used_at)}</td>
                    <td className="px-4 py-3 text-eleball-text-secondary">{formatTime(item.created_at)}</td>
                    <td className="px-4 py-3 text-eleball-text-secondary">{item.note || '-'}</td>
                    <td className="px-4 py-3 text-right no-print">
                      {!item.used && (
                        <button
                          onClick={() => handleDelete(item.id)}
                          className="text-eleball-error hover:underline"
                        >
                          删除
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
                {items.length === 0 && (
                  <tr>
                    <td colSpan={8} className="px-4 py-8 text-center text-eleball-text-secondary">
                      暂无兑换码，使用上方表单批量生成。
                    </td>
                  </tr>
                )}
              </tbody>
            </table>

            {/* 分页 */}
            <div className="flex items-center justify-between px-4 py-3 border-t border-eleball-outline">
              <div className="text-sm text-eleball-text-secondary">
                共 {total} 条，第 {filters.page} / {totalPages} 页
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => setFilters((prev) => ({ ...prev, page: Math.max(1, prev.page - 1) }))}
                  disabled={filters.page <= 1}
                  className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-50"
                >
                  上一页
                </button>
                <button
                  type="button"
                  onClick={() => setFilters((prev) => ({ ...prev, page: Math.min(totalPages, prev.page + 1) }))}
                  disabled={filters.page >= totalPages}
                  className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-50"
                >
                  下一页
                </button>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
