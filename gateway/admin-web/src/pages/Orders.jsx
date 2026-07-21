import { useEffect, useState } from 'react'
import { orderApi } from '../api/client'

const statusMap = {
  pending: { label: '待支付', className: 'bg-amber-50 text-amber-700' },
  paid: { label: '已支付', className: 'bg-emerald-50 text-emerald-700' },
  refunded: { label: '已退款', className: 'bg-gray-100 text-gray-600' },
  closed: { label: '已关闭', className: 'bg-gray-100 text-gray-500' }
}

const statusOptions = [
  { key: 'all', label: '全部' },
  { key: 'pending', label: '待支付' },
  { key: 'paid', label: '已支付' },
  { key: 'refunded', label: '已退款' },
  { key: 'closed', label: '已关闭' }
]

const channelMap = {
  wechat: '微信支付',
  alipay: '支付宝'
}

function formatDateTime(isoStr) {
  if (!isoStr) return '-'
  const d = new Date(isoStr)
  return isNaN(d.getTime()) ? isoStr : d.toLocaleString('zh-CN')
}

export default function Orders() {
  const [orders, setOrders] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [filterStatus, setFilterStatus] = useState('all')
  const [page, setPage] = useState(1)
  const pageSize = 10

  const fetchOrders = async () => {
    setLoading(true)
    try {
      const statusParam = filterStatus === 'all' ? '' : filterStatus
      const res = await orderApi.list(page, pageSize, statusParam)
      setOrders(res?.items || [])
      setTotal(res?.total || 0)
    } catch (err) {
      console.error('加载订单失败', err)
      alert('加载订单失败：' + (err?.message || err))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchOrders()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, filterStatus])

  const handleRefund = async (order) => {
    if (!window.confirm(`确认对订单 ${order.id} 执行退款？`)) return
    try {
      await orderApi.refund(order.id)
      fetchOrders()
    } catch (err) {
      alert('退款失败：' + (err?.message || err))
    }
  }

  const handleConfirm = async (order) => {
    if (!window.confirm(`确认已收到订单 ${order.id} 的款项？确认后将自动开通对应权益。`)) return
    try {
      await orderApi.confirm(order.id)
      fetchOrders()
    } catch (err) {
      alert('确认收款失败：' + (err?.message || err))
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">订单管理</h1>
          <p className="text-eleball-text-secondary mt-1">查看和处理用户支付订单</p>
        </div>
      </div>

      {/* 筛选 */}
      <div className="flex gap-2">
        {statusOptions.map(s => (
          <button
            key={s.key}
            onClick={() => { setFilterStatus(s.key); setPage(1) }}
            className={`px-4 py-2 rounded-xl text-sm font-medium transition-colors ${
              filterStatus === s.key
                ? 'bg-eleball-primary text-white'
                : 'bg-white border border-eleball-outline text-eleball-text hover:border-eleball-primary'
            }`}
          >
            {s.label}
          </button>
        ))}
      </div>

      {/* 订单表 */}
      <div className="card overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-eleball-outline">
              <th className="table-header">订单号</th>
              <th className="table-header">用户</th>
              <th className="table-header">支付渠道</th>
              <th className="table-header">金额</th>
              <th className="table-header">状态</th>
              <th className="table-header">创建时间</th>
              <th className="table-header">支付时间</th>
              <th className="table-header">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={8} className="table-cell text-center text-eleball-text-secondary">加载中...</td>
              </tr>
            ) : orders.length === 0 ? (
              <tr>
                <td colSpan={8} className="table-cell text-center text-eleball-text-secondary">暂无订单</td>
              </tr>
            ) : (
              orders.map((order) => {
                const status = statusMap[order.status]
                return (
                  <tr key={order.id} className="hover:bg-gray-50/50 transition-colors">
                    <td className="table-cell font-mono text-xs">{order.id}</td>
                    <td className="table-cell text-eleball-text-secondary">{order.user_id}</td>
                    <td className="table-cell">{channelMap[order.channel] || order.channel}</td>
                    <td className="table-cell font-medium">¥{((order.amount || 0) / 100).toFixed(2)}</td>
                    <td className="table-cell">
                      <span className={`inline-block px-2.5 py-1 rounded-full text-xs font-medium ${status?.className || 'bg-gray-100 text-gray-600'}`}>
                        {status?.label || order.status}
                      </span>
                    </td>
                    <td className="table-cell text-eleball-text-secondary text-xs">{formatDateTime(order.created_at)}</td>
                    <td className="table-cell text-eleball-text-secondary text-xs">{formatDateTime(order.paid_at)}</td>
                    <td className="table-cell">
                      <div className="flex gap-2">
                        <button className="text-sm text-eleball-primary hover:underline">详情</button>
                        {order.status === 'paid' && (
                          <button
                            onClick={() => handleRefund(order)}
                            className="text-sm text-red-500 hover:underline"
                          >
                            退款
                          </button>
                        )}
                        {order.status === 'pending' && (
                          <button
                            onClick={() => handleConfirm(order)}
                            className="text-sm text-emerald-600 hover:underline"
                          >
                            确认收款
                          </button>
                        )}
                      </div>
                    </td>
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
