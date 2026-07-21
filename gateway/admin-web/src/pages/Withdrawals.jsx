import { useState, useEffect } from 'react'
import client from '../api/client'

function Withdrawals() {
  const [records, setRecords] = useState([])
  const [loading, setLoading] = useState(true)
  const [filter, setFilter] = useState('pending')
  const [actionNote, setActionNote] = useState('')
  const [actionId, setActionId] = useState(null)

  const fetchRecords = async () => {
    setLoading(true)
    try {
      const resp = await client.get('/admin/withdrawals', { params: { status: filter, page: 1, page_size: 50 } })
      setRecords(resp.data?.items || [])
    } catch (err) {
      console.error('加载提现记录失败', err)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchRecords()
  }, [filter])

  const handleApprove = async (id) => {
    if (!window.confirm('确认通过该提现申请并发起付款？')) return
    try {
      await client.post(`/admin/withdrawals/${id}/approve`, { admin_note: actionNote })
      fetchRecords()
      setActionId(null)
      setActionNote('')
    } catch (err) {
      alert('操作失败: ' + (err.response?.data?.message || err.message))
    }
  }

  const handleReject = async (id) => {
    if (!window.confirm('确认拒绝该提现申请？余额将退回开发者账户。')) return
    try {
      await client.post(`/admin/withdrawals/${id}/reject`, { admin_note: actionNote })
      fetchRecords()
      setActionId(null)
      setActionNote('')
    } catch (err) {
      alert('操作失败: ' + (err.response?.data?.message || err.message))
    }
  }

  const statusBadge = (status) => {
    const styles = {
      pending: 'bg-yellow-100 text-yellow-800',
      approved: 'bg-blue-100 text-blue-800',
      completed: 'bg-green-100 text-green-800',
      rejected: 'bg-red-100 text-red-800',
      failed: 'bg-gray-100 text-gray-800',
    }
    const labels = {
      pending: '待审核',
      approved: '已通过',
      completed: '已完成',
      rejected: '已拒绝',
      failed: '付款失败',
    }
    return (
      <span className={`px-2 py-1 rounded-full text-xs font-medium ${styles[status] || styles.pending}`}>
        {labels[status] || status}
      </span>
    )
  }

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">提现审核</h1>

      {/* 筛选 */}
      <div className="flex gap-2">
        {['pending', 'approved', 'completed', 'rejected', ''].map((s) => (
          <button
            key={s || 'all'}
            onClick={() => setFilter(s)}
            className={`px-4 py-2 rounded-lg text-sm font-medium transition ${
              filter === s
                ? 'bg-blue-600 text-white'
                : 'bg-gray-100 text-gray-700 hover:bg-gray-200'
            }`}
          >
            {s === '' ? '全部' : { pending: '待审核', approved: '已通过', completed: '已完成', rejected: '已拒绝' }[s] || s}
          </button>
        ))}
      </div>

      {/* 列表 */}
      {loading ? (
        <div className="text-center py-12 text-gray-500">加载中...</div>
      ) : records.length === 0 ? (
        <div className="text-center py-12 text-gray-400">暂无记录</div>
      ) : (
        <div className="bg-white rounded-xl shadow overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="px-4 py-3 text-left font-medium text-gray-600">申请人</th>
                <th className="px-4 py-3 text-left font-medium text-gray-600">金额</th>
                <th className="px-4 py-3 text-left font-medium text-gray-600">渠道</th>
                <th className="px-4 py-3 text-left font-medium text-gray-600">收款账号</th>
                <th className="px-4 py-3 text-left font-medium text-gray-600">状态</th>
                <th className="px-4 py-3 text-left font-medium text-gray-600">时间</th>
                <th className="px-4 py-3 text-left font-medium text-gray-600">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-100">
              {records.map((r) => (
                <tr key={r.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3">{r.user_name || r.user_id}</td>
                  <td className="px-4 py-3 font-medium">¥{(r.amount / 100).toFixed(2)}</td>
                  <td className="px-4 py-3">{r.channel === 'wechat' ? '微信' : '支付宝'}</td>
                  <td className="px-4 py-3 text-gray-500">{r.account_info}</td>
                  <td className="px-4 py-3">{statusBadge(r.status)}</td>
                  <td className="px-4 py-3 text-gray-500">{new Date(r.created_at).toLocaleString()}</td>
                  <td className="px-4 py-3">
                    {r.status === 'pending' && (
                      <div className="flex gap-2">
                        {actionId === r.id ? (
                          <div className="flex flex-col gap-2">
                            <input
                              type="text"
                              placeholder="备注（可选）"
                              value={actionNote}
                              onChange={(e) => setActionNote(e.target.value)}
                              className="px-2 py-1 border rounded text-xs w-40"
                            />
                            <div className="flex gap-1">
                              <button
                                onClick={() => handleApprove(r.id)}
                                className="px-2 py-1 bg-green-600 text-white rounded text-xs hover:bg-green-700"
                              >
                                确认通过
                              </button>
                              <button
                                onClick={() => handleReject(r.id)}
                                className="px-2 py-1 bg-red-600 text-white rounded text-xs hover:bg-red-700"
                              >
                                确认拒绝
                              </button>
                              <button
                                onClick={() => { setActionId(null); setActionNote('') }}
                                className="px-2 py-1 bg-gray-200 text-gray-700 rounded text-xs"
                              >
                                取消
                              </button>
                            </div>
                          </div>
                        ) : (
                          <>
                            <button
                              onClick={() => setActionId(r.id)}
                              className="px-3 py-1 bg-blue-600 text-white rounded text-xs hover:bg-blue-700"
                            >
                              审核
                            </button>
                          </>
                        )}
                      </div>
                    )}
                    {r.admin_note && (
                      <div className="text-xs text-gray-500 mt-1">备注: {r.admin_note}</div>
                    )}
                    {r.tx_id && (
                      <div className="text-xs text-gray-500">流水: {r.tx_id}</div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

export default Withdrawals
