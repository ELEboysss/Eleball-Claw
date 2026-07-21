import { useEffect, useState } from 'react'
import { billingApi } from '../api/client'

// claw 我的计费（替代云端 Billing）。
// 不展示"今日总收入/平台累计收入"等平台数据，只展示当前用户云端账户余额与充值记录。
// claw 本地不计费；弹丸余额与充值均在云端 eleball.cn。
// 见 docs/marketing/claw-implementation-plan.md §D.1。
export default function Billing() {
  const [balance, setBalance] = useState(null)
  const [history, setHistory] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    Promise.all([
      billingApi.getBalance().catch(() => null),
      billingApi.getRechargeHistory(1, 20).catch(() => null)
    ])
      .then(([bal, hist]) => {
        setBalance(bal)
        setHistory(hist?.items || hist || [])
      })
      .catch((err) => setError(typeof err === 'string' ? err : (err?.message || '加载失败')))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return <div className="text-center py-8 text-sm text-eleball-text-secondary">加载中…</div>

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">我的计费</h1>
        <p className="text-sm text-eleball-text-secondary mt-1">
          云端账户弹丸余额与充值记录（claw 本地不计费）
        </p>
      </div>

      {error && <div className="p-3 rounded-xl bg-red-50 text-red-600 text-sm">{error}</div>}

      {/* 余额卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="card p-5">
          <div className="text-xs text-eleball-text-secondary mb-1">弹丸余额</div>
          <div className="text-2xl font-bold text-eleball-primary">
            {balance?.danwan != null ? Number(balance.danwan).toLocaleString('zh-CN') : '-'}
            <span className="text-sm text-eleball-text-secondary ml-1">分</span>
          </div>
        </div>
        <div className="card p-5">
          <div className="text-xs text-eleball-text-secondary mb-1">优雅弹丸</div>
          <div className="text-2xl font-bold">
            {balance?.elegant != null ? Number(balance.elegant).toLocaleString('zh-CN') : '-'}
            <span className="text-sm text-eleball-text-secondary ml-1">分</span>
          </div>
        </div>
      </div>

      {/* 充值记录 */}
      <div className="card p-6">
        <h2 className="text-lg font-semibold mb-4">充值记录</h2>
        {history.length === 0 ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">暂无充值记录</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left">
              <thead className="text-xs text-eleball-text-secondary border-b border-eleball-outline">
                <tr>
                  <th className="px-3 py-2 font-medium">时间</th>
                  <th className="px-3 py-2 font-medium">金额</th>
                  <th className="px-3 py-2 font-medium">渠道</th>
                  <th className="px-3 py-2 font-medium">说明</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-eleball-outline-variant">
                {history.map((h, i) => (
                  <tr key={h.id || i}>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">
                      {h.created_at ? new Date(h.created_at).toLocaleString('zh-CN') : '-'}
                    </td>
                    <td className="px-3 py-2.5 text-emerald-600">
                      +{Number(h.amount || 0).toLocaleString('zh-CN')} 弹丸
                    </td>
                    <td className="px-3 py-2.5 text-xs">{h.channel || h.payment_channel || '-'}</td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">{h.description || '-'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
