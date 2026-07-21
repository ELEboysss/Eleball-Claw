import { useEffect, useState } from 'react'
import { userApi, billingApi, vipApi } from '../api/client'

// 后端 status 为数字：1=正常，0=禁用；前端统一用字符串便于展示与筛选
const STATUS_ACTIVE = 'active'
const STATUS_INACTIVE = 'inactive'

const statusMap = {
  [STATUS_ACTIVE]: { label: '正常', className: 'bg-emerald-50 text-emerald-700' },
  [STATUS_INACTIVE]: { label: '禁用', className: 'bg-gray-100 text-gray-600' }
}

const statusOptions = [
  { value: '', label: '全部' },
  { value: STATUS_ACTIVE, label: '正常' },
  { value: STATUS_INACTIVE, label: '禁用' }
]

const currencyOptions = [
  { value: 'danwan', label: '弹丸' },
  { value: 'elegant', label: '优雅弹丸' }
]

function formatDateTime(isoStr) {
  if (!isoStr) return '-'
  const d = new Date(isoStr)
  return isNaN(d.getTime()) ? isoStr : d.toLocaleString('zh-CN')
}

function formatDate(isoStr) {
  if (!isoStr) return '-'
  const d = new Date(isoStr)
  return isNaN(d.getTime()) ? isoStr : d.toLocaleDateString('zh-CN')
}

function formatMoney(cents) {
  if (cents === undefined || cents === null) return '-'
  return '¥' + (Number(cents) / 100).toFixed(2)
}

function fenToYuan(fen) {
  return ((fen || 0) / 100).toFixed(2)
}

// 弹丸/优雅弹丸余额直接显示个数（后端按 1 弹丸 = 1 分存储，数值即弹丸数量）
function formatCurrency(cents, currency = 'danwan') {
  if (cents === undefined || cents === null) return '-'
  const unit = currency === 'elegant' ? '优雅弹丸' : '弹丸'
  return Number(cents).toLocaleString('zh-CN') + ' ' + unit
}

export default function Users() {
  const [users, setUsers] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(false)
  const [search, setSearch] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [page, setPage] = useState(1)
  const pageSize = 10

  // 充值弹窗状态
  const [rechargeUser, setRechargeUser] = useState(null)
  const [rechargeAmount, setRechargeAmount] = useState('')
  const [rechargeCurrency, setRechargeCurrency] = useState('danwan')
  const [rechargeLoading, setRechargeLoading] = useState(false)

  // VIP 开通/续期弹窗状态
  const [grantUser, setGrantUser] = useState(null)
  const [vipPlans, setVipPlans] = useState([])
  const [grantPlanId, setGrantPlanId] = useState('')
  const [grantMonths, setGrantMonths] = useState(1)
  const [grantLoading, setGrantLoading] = useState(false)

  const fetchUsers = async () => {
    setLoading(true)
    try {
      // 状态筛选：active/inactive 映射为后端 1/0
      const statusParam = statusFilter === STATUS_ACTIVE ? 1 : statusFilter === STATUS_INACTIVE ? 0 : undefined
      const res = await userApi.list(page, pageSize, search, statusParam)
      setUsers(res?.items || [])
      setTotal(res?.total || 0)
    } catch (err) {
      console.error('加载用户列表失败', err)
      alert('加载用户列表失败：' + (err?.message || err))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchUsers()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, statusFilter])

  const handleSearch = (e) => {
    e.preventDefault()
    setPage(1)
    fetchUsers()
  }

  const handleStatusChange = async (user, newStatusStr) => {
    const newStatus = newStatusStr === STATUS_ACTIVE ? 1 : 0
    if (!window.confirm(`确认将用户 "${user.nickname || user.username}" 状态设置为「${statusMap[newStatusStr]?.label || newStatusStr}」？`)) return
    try {
      await userApi.updateStatus(user.id, newStatus)
      fetchUsers()
    } catch (err) {
      alert('状态更新失败：' + (err?.message || err))
    }
  }

  const handleDelete = async (user) => {
    if (!window.confirm(`确认删除用户 "${user.nickname || user.username}"？此操作不可恢复。`)) return
    try {
      await userApi.delete(user.id)
      fetchUsers()
    } catch (err) {
      alert('删除失败：' + (err?.message || err))
    }
  }

  const openRecharge = (user) => {
    setRechargeUser(user)
    setRechargeAmount('')
    setRechargeCurrency('danwan')
  }

  const closeRecharge = () => {
    setRechargeUser(null)
    setRechargeAmount('')
    setRechargeCurrency('danwan')
  }

  const openGrantVIP = async (user) => {
    setGrantUser(user)
    setGrantPlanId('')
    setGrantMonths(1)
    try {
      const res = await vipApi.listPlans()
      const plans = (res?.items || []).filter((p) => p.level > 0)
      setVipPlans(plans)
      if (plans.length > 0) {
        setGrantPlanId(plans[0].id)
      }
    } catch (err) {
      alert('加载 VIP 套餐失败：' + (err?.message || err))
    }
  }

  const closeGrantVIP = () => {
    setGrantUser(null)
    setGrantPlanId('')
    setGrantMonths(1)
  }

  const handleGrantVIP = async (e) => {
    e.preventDefault()
    if (!grantUser || !grantPlanId) return
    setGrantLoading(true)
    try {
      await vipApi.grant(grantUser.id, grantPlanId, grantMonths)
      alert('VIP 开通/续期成功')
      closeGrantVIP()
      fetchUsers()
    } catch (err) {
      alert('VIP 开通失败：' + (err?.message || err))
    } finally {
      setGrantLoading(false)
    }
  }

  const handleRecharge = async (e) => {
    e.preventDefault()
    if (!rechargeUser) return
    const amount = Number(rechargeAmount)
    if (!amount || amount <= 0) {
      alert('请输入有效的充值金额（正整数，单位：分）')
      return
    }
    setRechargeLoading(true)
    try {
      await billingApi.recharge(rechargeUser.id, amount * 100, rechargeCurrency)
      alert('充值成功')
      closeRecharge()
      fetchUsers()
    } catch (err) {
      alert('充值失败：' + (err?.message || err))
    } finally {
      setRechargeLoading(false)
    }
  }

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">用户管理</h1>
          <p className="text-eleball-text-secondary mt-1">查看、管理和维护平台用户</p>
        </div>
      </div>

      {/* 搜索与筛选 */}
      <form onSubmit={handleSearch} className="flex flex-wrap items-center gap-3">
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索用户名或昵称..."
          className="input max-w-xs"
        />
        <select
          value={statusFilter}
          onChange={(e) => { setStatusFilter(e.target.value); setPage(1) }}
          className="input max-w-xs"
        >
          {statusOptions.map((opt) => (
            <option key={opt.value} value={opt.value}>{opt.label}</option>
          ))}
        </select>
        <button type="submit" className="btn-secondary">搜索</button>
      </form>

      {/* 用户表格 */}
      <div className="card overflow-x-auto">
        <table className="w-full">
          <thead>
            <tr className="border-b border-eleball-outline">
              <th className="table-header">用户</th>
              <th className="table-header">角色</th>
              <th className="table-header">状态</th>
              <th className="table-header">弹丸余额</th>
              <th className="table-header">优雅弹丸</th>
              <th className="table-header">累计充值</th>
              <th className="table-header">VIP</th>
              <th className="table-header">注册时间</th>
              <th className="table-header">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr>
                <td colSpan={8} className="table-cell text-center text-eleball-text-secondary">加载中...</td>
              </tr>
            ) : users.length === 0 ? (
              <tr>
                <td colSpan={8} className="table-cell text-center text-eleball-text-secondary">暂无数据</td>
              </tr>
            ) : (
              users.map((user) => {
                // 后端 status 数字 -> 前端字符串
                const statusStr = user.status === 1 ? STATUS_ACTIVE : STATUS_INACTIVE
                const status = statusMap[statusStr]
                return (
                  <tr key={user.id} className="hover:bg-gray-50/50 transition-colors">
                    <td className="table-cell">
                      <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-full bg-eleball-primary/10 flex items-center justify-center text-eleball-primary font-bold text-sm">
                          {(user.nickname || user.username || '?')[0]}
                        </div>
                        <div>
                          <p className="font-medium">{user.nickname || user.username}</p>
                          <p className="text-xs text-eleball-text-secondary">{user.username}</p>
                        </div>
                      </div>
                    </td>
                    <td className="table-cell text-eleball-text-secondary text-sm capitalize">{user.role || 'user'}</td>
                    <td className="table-cell">
                      <span className={`inline-block px-2.5 py-1 rounded-full text-xs font-medium ${status.className}`}>
                        {status.label}
                      </span>
                    </td>
                    <td className="table-cell font-medium">{formatCurrency(user.balance, 'danwan')}</td>
                    <td className="table-cell font-medium">{formatCurrency(user.elegant_balance, 'elegant')}</td>
                    <td className="table-cell font-medium text-emerald-600">{formatMoney(user.total_recharged)}</td>
                    <td className="table-cell text-sm">
                      {user.role === 'admin' ? (
                        <span className="font-medium text-eleball-primary">管理员</span>
                      ) : user.vip_level > 0 ? (
                        <div>
                          <span className="font-medium text-eleball-primary">VIP{user.vip_level}</span>
                          <span className="text-xs text-eleball-text-secondary block">{formatDate(user.vip_expire_at)} 到期</span>
                        </div>
                      ) : (
                        <span className="text-eleball-text-secondary">小弹丸（免费）</span>
                      )}
                    </td>
                    <td className="table-cell text-eleball-text-secondary">{formatDate(user.created_at)}</td>
                    <td className="table-cell">
                      <div className="flex items-center gap-3">
                        <button
                          onClick={() => openRecharge(user)}
                          className="text-sm text-emerald-600 hover:underline"
                        >
                          充值
                        </button>
                        <button
                          onClick={() => openGrantVIP(user)}
                          className="text-sm text-amber-600 hover:underline"
                        >
                          VIP
                        </button>
                        <button
                          onClick={() => handleStatusChange(user, statusStr === STATUS_ACTIVE ? STATUS_INACTIVE : STATUS_ACTIVE)}
                          className="text-sm text-eleball-primary hover:underline"
                        >
                          {statusStr === STATUS_ACTIVE ? '禁用' : '启用'}
                        </button>
                        <button
                          onClick={() => handleDelete(user)}
                          className="text-sm text-red-500 hover:underline"
                        >
                          删除
                        </button>
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

      {/* 充值弹窗 */}
      {rechargeUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="bg-white rounded-2xl shadow-xl w-full max-w-md p-6 space-y-4">
            <h3 className="text-lg font-bold">为用户充值</h3>
            <p className="text-sm text-eleball-text-secondary">
              用户：{rechargeUser.nickname || rechargeUser.username}（{rechargeUser.id}）
            </p>
            <form onSubmit={handleRecharge} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1">货币类型</label>
                <select
                  value={rechargeCurrency}
                  onChange={(e) => setRechargeCurrency(e.target.value)}
                  className="input w-full"
                >
                  {currencyOptions.map((opt) => (
                    <option key={opt.value} value={opt.value}>{opt.label}</option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1">充值金额（元）</label>
                <input
                  type="number"
                  min="0.01"
                  step="0.01"
                  value={rechargeAmount}
                  onChange={(e) => setRechargeAmount(e.target.value)}
                  placeholder="例如：50"
                  className="input w-full"
                  required
                />
                <p className="text-xs text-eleball-text-secondary mt-1">1 元 = 100 弹丸/优雅弹丸</p>
              </div>
              <div className="flex gap-3 pt-2">
                <button
                  type="button"
                  onClick={closeRecharge}
                  className="flex-1 px-4 py-2 rounded-xl border border-eleball-outline text-sm font-medium hover:bg-gray-50"
                >
                  取消
                </button>
                <button
                  type="submit"
                  disabled={rechargeLoading}
                  className="flex-1 px-4 py-2 rounded-xl bg-eleball-primary text-white text-sm font-medium hover:bg-eleball-primary/90 disabled:opacity-50"
                >
                  {rechargeLoading ? '充值中...' : '确认充值'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* VIP 开通/续期弹窗 */}
      {grantUser && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <div className="bg-white rounded-2xl shadow-xl w-full max-w-md p-6 space-y-4">
            <h3 className="text-lg font-bold">为用户开通/续期 VIP</h3>
            <p className="text-sm text-eleball-text-secondary">
              用户：{grantUser.nickname || grantUser.username}（{grantUser.id}）
            </p>
            <form onSubmit={handleGrantVIP} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1">选择套餐</label>
                <select
                  value={grantPlanId}
                  onChange={(e) => setGrantPlanId(e.target.value)}
                  className="input w-full"
                  required
                >
                  <option value="">请选择</option>
                  {vipPlans.map((plan) => (
                    <option key={plan.id} value={plan.id}>
                      {plan.name}（VIP{plan.level} / ¥{fenToYuan(plan.price_fen)} / {plan.duration_days}天）
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1">月数</label>
                <input
                  type="number"
                  min="1"
                  step="1"
                  value={grantMonths}
                  onChange={(e) => setGrantMonths(Number(e.target.value))}
                  className="input w-full"
                  required
                />
              </div>
              <div className="flex gap-3 pt-2">
                <button
                  type="button"
                  onClick={closeGrantVIP}
                  className="flex-1 px-4 py-2 rounded-xl border border-eleball-outline text-sm font-medium hover:bg-gray-50"
                >
                  取消
                </button>
                <button
                  type="submit"
                  disabled={grantLoading}
                  className="flex-1 px-4 py-2 rounded-xl bg-amber-500 text-white text-sm font-medium hover:bg-amber-600 disabled:opacity-50"
                >
                  {grantLoading ? '开通中...' : '确认开通'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
