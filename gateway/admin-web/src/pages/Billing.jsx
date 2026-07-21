import { useEffect, useState, useMemo } from 'react'
import { billingApi, dashboardApi } from '../api/client'

// 后端 type 为 consume/recharge/refund，前端展示统一为 consumption/recharge/refund
const typeMap = {
  consumption: { label: '消费', className: 'text-red-500' },
  recharge: { label: '充值', className: 'text-emerald-600' },
  refund: { label: '退款', className: 'text-eleball-secondary' }
}

const backendToFrontendType = {
  consume: 'consumption',
  consumption: 'consumption',
  recharge: 'recharge',
  refund: 'refund'
}

const filterOptions = [
  { key: 'all', label: '全部' },
  { key: 'consumption', label: '消费' },
  { key: 'recharge', label: '充值' },
  { key: 'refund', label: '退款' }
]

// 前端筛选类型 -> 后端查询 type
const frontendToBackendType = {
  consumption: 'consume',
  recharge: 'recharge',
  refund: 'refund'
}

// 默认单价：弹丸 / 1M tokens，当交易记录中缺少用量信息时按 50K tokens 估算
const DEFAULT_PRICE_PER_1M_TOKENS = 100

function formatDateTime(isoStr) {
  if (!isoStr) return '-'
  const d = new Date(isoStr)
  return isNaN(d.getTime()) ? isoStr : d.toLocaleString('zh-CN')
}

// 将交易金额显示为弹丸/优雅弹丸；后端 consume 金额已按 token 用量 × 单价计算
// 若记录缺失金额，则按 50K tokens × 默认单价估算
function formatTransactionAmount(amount, currency = 'danwan') {
  if (amount === undefined || amount === null) {
    const fallbackTokens = 50000
    const estimated = Math.round((fallbackTokens * DEFAULT_PRICE_PER_1M_TOKENS) / 1000000)
    const unit = currency === 'elegant' ? '优雅弹丸' : '弹丸'
    return `-${estimated} ${unit}`
  }
  const unit = currency === 'elegant' ? '优雅弹丸' : '弹丸'
  return `${amount > 0 ? '+' : ''}${Number(amount).toLocaleString('zh-CN')} ${unit}`
}

export default function Billing() {
  const [transactions, setTransactions] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [filterType, setFilterType] = useState('all')
  const [stats, setStats] = useState(null)
  const [page, setPage] = useState(1)
  const pageSize = 10

  const fetchData = async () => {
    setLoading(true)
    try {
      const backendType = filterType === 'all' ? '' : frontendToBackendType[filterType]
      const [txRes, statsRes] = await Promise.all([
        billingApi.listTransactions(page, pageSize, backendType),
        dashboardApi.getStats()
      ])
      setTransactions(txRes?.items || [])
      setTotal(txRes?.total || 0)
      setStats(statsRes || {})
    } catch (err) {
      console.error('加载计费数据失败', err)
      alert('加载计费数据失败：' + (err?.message || err))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchData()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, filterType])

  // 今日总消耗：由列表中今天的 consume 记录汇总（页面可见范围内）
  const todayConsumption = useMemo(() => {
    const today = new Date().toDateString()
    return transactions
      .filter((t) => backendToFrontendType[t.type] === 'consumption' && new Date(t.created_at).toDateString() === today)
      .reduce((sum, t) => sum + Math.abs(t.amount || 0), 0)
  }, [transactions])

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">计费管理</h1>
          <p className="text-eleball-text-secondary mt-1">查看 Token 消耗、充值记录和余额变动</p>
        </div>
      </div>

      {/* 统计 */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <div className="card">
          <p className="text-sm text-eleball-text-secondary">今日总收入</p>
          <p className="text-2xl font-bold mt-2 text-emerald-600">¥{((stats?.today_revenue || 0) / 100).toFixed(2)}</p>
        </div>
        <div className="card">
          <p className="text-sm text-eleball-text-secondary">今日总消耗</p>
          <p className="text-2xl font-bold mt-2 text-red-500">{todayConsumption.toLocaleString('zh-CN')} 弹丸</p>
        </div>
        <div className="card">
          <p className="text-sm text-eleball-text-secondary">平台累计收入</p>
          <p className="text-2xl font-bold mt-2">¥{((stats?.total_revenue || 0) / 100).toFixed(2)}</p>
        </div>
      </div>

      {/* 筛选 */}
      <div className="flex gap-2">
        {filterOptions.map(t => (
          <button
            key={t.key}
            onClick={() => { setFilterType(t.key); setPage(1) }}
            className={`px-4 py-2 rounded-xl text-sm font-medium transition-colors ${
              filterType === t.key
                ? 'bg-eleball-primary text-white'
                : 'bg-white border border-eleball-outline text-eleball-text hover:border-eleball-primary'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* 交易记录表 */}
      <div className="card overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-eleball-outline">
              <th className="table-header">交易 ID</th>
              <th className="table-header">用户</th>
              <th className="table-header">类型</th>
              <th className="table-header">金额（弹丸）</th>
              <th className="table-header">说明</th>
              <th className="table-header">时间</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={6} className="table-cell text-center text-eleball-text-secondary">加载中...</td>
              </tr>
            ) : transactions.length === 0 ? (
              <tr>
                <td colSpan={6} className="table-cell text-center text-eleball-text-secondary">暂无记录</td>
              </tr>
            ) : (
              transactions.map((tx) => {
                const frontendType = backendToFrontendType[tx.type] || tx.type
                const type = typeMap[frontendType] || { label: tx.type, className: 'text-gray-500' }
                const amount = tx.amount || 0
                return (
                  <tr key={tx.id} className="hover:bg-gray-50/50 transition-colors">
                    <td className="table-cell font-mono text-xs">{tx.id}</td>
                    <td className="table-cell text-eleball-text-secondary">{tx.user_id}</td>
                    <td className="table-cell">
                      <span className={`text-xs font-medium ${type.className}`}>{type.label}</span>
                    </td>
                    <td className={`table-cell font-medium ${amount > 0 ? 'text-emerald-600' : 'text-red-500'}`}>
                      {formatTransactionAmount(amount, tx.currency)}
                    </td>
                    <td className="table-cell text-eleball-text-secondary">{tx.description || '-'}</td>
                    <td className="table-cell text-eleball-text-secondary text-xs">{formatDateTime(tx.created_at)}</td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>

      {/* 分页 */}
      <div className="flex items-center justify-between">
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
    </div>
  )
}
