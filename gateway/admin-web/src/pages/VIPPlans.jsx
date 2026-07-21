import { useEffect, useState } from 'react'
import { vipApi } from '../api/client'

function fenToYuan(fen) {
  return ((fen || 0) / 100).toFixed(2)
}

function yuanToFen(yuan) {
  return Math.round(parseFloat(yuan || '0') * 100)
}

function formatDate(isoStr) {
  if (!isoStr) return '-'
  const d = new Date(isoStr)
  return isNaN(d.getTime()) ? isoStr : d.toLocaleString('zh-CN')
}

const emptyPlan = {
  level: 1,
  name: '',
  price_yuan: '',
  duration_days: 30,
  discount_percent: 100,
  max_conversations: 100,
  max_agent_sessions: 10,
  asr_quota_monthly: 1000,
  agent_enabled: true,
  file_tools_enabled: true,
  sort_order: 0,
  is_enabled: true,
  description: ''
}

export default function VIPPlans() {
  const [tab, setTab] = useState('plans')
  const [plans, setPlans] = useState([])
  const [subscriptions, setSubscriptions] = useState([])
  const [subTotal, setSubTotal] = useState(0)
  const [subUserId, setSubUserId] = useState('')
  const [subPage, setSubPage] = useState(1)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState(null)
  const [form, setForm] = useState(emptyPlan)

  const fetchPlans = async () => {
    setLoading(true)
    setError('')
    try {
      const res = await vipApi.listPlans()
      setPlans(res?.items || [])
    } catch (err) {
      setError(err?.message || err || '加载套餐失败')
    } finally {
      setLoading(false)
    }
  }

  const fetchSubscriptions = async (page = 1) => {
    setLoading(true)
    setError('')
    try {
      const res = await vipApi.listSubscriptions(page, 20, subUserId)
      setSubscriptions(res?.items || [])
      setSubTotal(res?.total || 0)
      setSubPage(page)
    } catch (err) {
      setError(err?.message || err || '加载订阅记录失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchPlans()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    if (tab === 'subscriptions') {
      fetchSubscriptions(1)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab])

  const handleChange = (key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    try {
      const body = {
        level: Number(form.level) || 0,
        name: form.name,
        price_fen: yuanToFen(form.price_yuan),
        duration_days: Number(form.duration_days) || 30,
        discount_percent: Number(form.discount_percent) || 100,
        max_conversations: Number(form.max_conversations) || 0,
        max_agent_sessions: Number(form.max_agent_sessions) || 0,
        asr_quota_monthly: Number(form.asr_quota_monthly) || 0,
        agent_enabled: Boolean(form.agent_enabled),
        file_tools_enabled: Boolean(form.file_tools_enabled),
        sort_order: Number(form.sort_order) || 0,
        is_enabled: Boolean(form.is_enabled),
        description: form.description
      }
      if (editing) {
        await vipApi.updatePlan(editing.id, body)
      } else {
        await vipApi.createPlan(body)
      }
      setShowForm(false)
      setEditing(null)
      setForm(emptyPlan)
      fetchPlans()
    } catch (err) {
      setError(err?.message || err || '提交失败')
    }
  }

  const handleEdit = (item) => {
    setEditing(item)
    setForm({
      level: item.level,
      name: item.name || '',
      price_yuan: fenToYuan(item.price_fen),
      duration_days: item.duration_days || 30,
      discount_percent: item.discount_percent ?? 100,
      max_conversations: item.max_conversations ?? 100,
      max_agent_sessions: item.max_agent_sessions ?? 10,
      asr_quota_monthly: item.asr_quota_monthly ?? 1000,
      agent_enabled: item.agent_enabled,
      file_tools_enabled: item.file_tools_enabled,
      sort_order: item.sort_order || 0,
      is_enabled: item.is_enabled,
      description: item.description || ''
    })
    setShowForm(true)
  }

  const handleDelete = async (id) => {
    if (!window.confirm('确定删除该 VIP 套餐？')) return
    try {
      await vipApi.deletePlan(id)
      fetchPlans()
    } catch (err) {
      setError(err?.message || err || '删除失败')
    }
  }

  const handleAdd = () => {
    setEditing(null)
    setForm(emptyPlan)
    setShowForm(true)
  }

  const totalSubPages = Math.max(1, Math.ceil(subTotal / 20))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">VIP 会员管理</h1>
          <p className="text-eleball-text-secondary mt-1">配置会员套餐、查看订阅记录</p>
        </div>
        {tab === 'plans' && (
          <button onClick={handleAdd} className="btn-primary">新增套餐</button>
        )}
      </div>

      <div className="flex items-center gap-2 border-b border-eleball-outline">
        <button
          onClick={() => setTab('plans')}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            tab === 'plans'
              ? 'border-eleball-primary text-eleball-primary'
              : 'border-transparent text-eleball-text-secondary hover:text-eleball-text'
          }`}
        >
          套餐配置
        </button>
        <button
          onClick={() => setTab('subscriptions')}
          className={`px-4 py-2 text-sm font-medium border-b-2 transition-colors ${
            tab === 'subscriptions'
              ? 'border-eleball-primary text-eleball-primary'
              : 'border-transparent text-eleball-text-secondary hover:text-eleball-text'
          }`}
        >
          订阅记录
        </button>
      </div>

      {error && (
        <div className="text-sm text-eleball-error bg-red-50 rounded-xl px-4 py-3">{error}</div>
      )}

      {tab === 'plans' && (
        <>
          {showForm && (
            <div className="card space-y-4">
              <h3 className="text-base font-semibold">{editing ? '编辑套餐' : '新增套餐'}</h3>
              <form onSubmit={handleSubmit} className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <div>
                  <label className="block text-sm font-medium mb-1.5">等级</label>
                  <input
                    type="number"
                    min="0"
                    value={form.level}
                    onChange={(e) => handleChange('level', e.target.value)}
                    className="input"
                    required
                  />
                  <p className="text-xs text-eleball-text-secondary mt-1">0 为默认小弹丸，不对外展示</p>
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">套餐名称</label>
                  <input
                    type="text"
                    value={form.name}
                    onChange={(e) => handleChange('name', e.target.value)}
                    placeholder="如：强力弹丸"
                    className="input"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">月卡价格（元）</label>
                  <input
                    type="number"
                    step="0.01"
                    min="0"
                    value={form.price_yuan}
                    onChange={(e) => handleChange('price_yuan', e.target.value)}
                    className="input"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">周期（天）</label>
                  <input
                    type="number"
                    min="1"
                    value={form.duration_days}
                    onChange={(e) => handleChange('duration_days', e.target.value)}
                    className="input"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">计费折扣（%）</label>
                  <input
                    type="number"
                    min="1"
                    max="100"
                    value={form.discount_percent}
                    onChange={(e) => handleChange('discount_percent', e.target.value)}
                    className="input"
                    required
                  />
                  <p className="text-xs text-eleball-text-secondary mt-1">80 表示 8 折</p>
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">排序</label>
                  <input
                    type="number"
                    value={form.sort_order}
                    onChange={(e) => handleChange('sort_order', e.target.value)}
                    className="input"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">最大对话数</label>
                  <input
                    type="number"
                    min="0"
                    value={form.max_conversations}
                    onChange={(e) => handleChange('max_conversations', e.target.value)}
                    className="input"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">最大 Agent Session</label>
                  <input
                    type="number"
                    min="0"
                    value={form.max_agent_sessions}
                    onChange={(e) => handleChange('max_agent_sessions', e.target.value)}
                    className="input"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">ASR 月额度</label>
                  <input
                    type="number"
                    min="0"
                    value={form.asr_quota_monthly}
                    onChange={(e) => handleChange('asr_quota_monthly', e.target.value)}
                    className="input"
                  />
                </div>
                <div className="md:col-span-3">
                  <label className="block text-sm font-medium mb-1.5">描述</label>
                  <input
                    type="text"
                    value={form.description}
                    onChange={(e) => handleChange('description', e.target.value)}
                    className="input"
                  />
                </div>
                <div className="flex items-center gap-6 md:col-span-3">
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={form.is_enabled}
                      onChange={(e) => handleChange('is_enabled', e.target.checked)}
                      className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                    />
                    <span className="text-sm">上架</span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={form.agent_enabled}
                      onChange={(e) => handleChange('agent_enabled', e.target.checked)}
                      className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                    />
                    <span className="text-sm">允许 Agent 模式</span>
                  </label>
                  <label className="flex items-center gap-2 cursor-pointer">
                    <input
                      type="checkbox"
                      checked={form.file_tools_enabled}
                      onChange={(e) => handleChange('file_tools_enabled', e.target.checked)}
                      className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                    />
                    <span className="text-sm">允许文件服务器工具</span>
                  </label>
                </div>
                <div className="flex gap-3 md:col-span-3">
                  <button type="submit" className="btn-primary">{editing ? '保存' : '创建'}</button>
                  <button
                    type="button"
                    onClick={() => {
                      setShowForm(false)
                      setEditing(null)
                      setForm(emptyPlan)
                    }}
                    className="btn-secondary"
                  >
                    取消
                  </button>
                </div>
              </form>
            </div>
          )}

          <div className="card overflow-hidden">
            {loading ? (
              <div className="p-8 text-sm text-eleball-text-secondary">加载中...</div>
            ) : (
              <table className="w-full text-sm">
                <thead className="bg-gray-50 text-eleball-text-secondary">
                  <tr>
                    <th className="text-left px-4 py-3 font-medium">等级</th>
                    <th className="text-left px-4 py-3 font-medium">名称</th>
                    <th className="text-left px-4 py-3 font-medium">月卡价</th>
                    <th className="text-left px-4 py-3 font-medium">折扣</th>
                    <th className="text-left px-4 py-3 font-medium">对话 / Session / ASR</th>
                    <th className="text-left px-4 py-3 font-medium">功能</th>
                    <th className="text-left px-4 py-3 font-medium">状态</th>
                    <th className="text-right px-4 py-3 font-medium">操作</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-eleball-outline">
                  {plans.length === 0 && (
                    <tr>
                      <td colSpan={8} className="px-4 py-8 text-center text-eleball-text-secondary">暂无套餐</td>
                    </tr>
                  )}
                  {plans.map((item) => (
                    <tr key={item.id} className="hover:bg-gray-50">
                      <td className="px-4 py-3">VIP{item.level}</td>
                      <td className="px-4 py-3">
                        <div className="font-medium text-eleball-text">{item.name}</div>
                        {item.description && (
                          <div className="text-xs text-eleball-text-secondary">{item.description}</div>
                        )}
                      </td>
                      <td className="px-4 py-3">¥{fenToYuan(item.price_fen)}</td>
                      <td className="px-4 py-3">{item.discount_percent}%</td>
                      <td className="px-4 py-3">
                        {item.max_conversations} / {item.max_agent_sessions} / {item.asr_quota_monthly}
                      </td>
                      <td className="px-4 py-3 text-eleball-text-secondary text-xs">
                        {item.agent_enabled ? 'Agent' : ''}
                        {item.agent_enabled && item.file_tools_enabled ? ' · ' : ''}
                        {item.file_tools_enabled ? '文件工具' : ''}
                      </td>
                      <td className="px-4 py-3">
                        <span
                          className={`inline-flex px-2 py-1 rounded-lg text-xs font-medium ${
                            item.is_enabled
                              ? 'bg-emerald-50 text-emerald-700'
                              : 'bg-gray-100 text-gray-600'
                          }`}
                        >
                          {item.is_enabled ? '上架' : '下架'}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-right">
                        <div className="flex items-center justify-end gap-3">
                          <button onClick={() => handleEdit(item)} className="text-sm text-eleball-primary hover:underline">编辑</button>
                          <button onClick={() => handleDelete(item.id)} className="text-sm text-red-500 hover:underline">删除</button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </>
      )}

      {tab === 'subscriptions' && (
        <>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              fetchSubscriptions(1)
            }}
            className="flex items-center gap-3"
          >
            <input
              type="text"
              value={subUserId}
              onChange={(e) => setSubUserId(e.target.value)}
              placeholder="按用户 ID 筛选"
              className="input max-w-xs"
            />
            <button type="submit" className="btn-secondary">搜索</button>
          </form>

          <div className="card overflow-hidden">
            {loading ? (
              <div className="p-8 text-sm text-eleball-text-secondary">加载中...</div>
            ) : (
              <table className="w-full text-sm">
                <thead className="bg-gray-50 text-eleball-text-secondary">
                  <tr>
                    <th className="text-left px-4 py-3 font-medium">用户 ID</th>
                    <th className="text-left px-4 py-3 font-medium">等级</th>
                    <th className="text-left px-4 py-3 font-medium">时长</th>
                    <th className="text-left px-4 py-3 font-medium">有效期</th>
                    <th className="text-left px-4 py-3 font-medium">状态</th>
                    <th className="text-left px-4 py-3 font-medium">创建时间</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-eleball-outline">
                  {subscriptions.length === 0 && (
                    <tr>
                      <td colSpan={6} className="px-4 py-8 text-center text-eleball-text-secondary">暂无订阅记录</td>
                    </tr>
                  )}
                  {subscriptions.map((item) => (
                    <tr key={item.id} className="hover:bg-gray-50">
                      <td className="px-4 py-3 font-mono text-xs">{item.user_id}</td>
                      <td className="px-4 py-3">VIP{item.level}</td>
                      <td className="px-4 py-3">{item.duration_days} 天</td>
                      <td className="px-4 py-3">{formatDate(item.started_at)} 至 {formatDate(item.expires_at)}</td>
                      <td className="px-4 py-3">
                        <span
                          className={`inline-flex px-2 py-1 rounded-lg text-xs font-medium ${
                            item.status === 'active'
                              ? 'bg-emerald-50 text-emerald-700'
                              : 'bg-gray-100 text-gray-600'
                          }`}
                        >
                          {item.status === 'active' ? '生效中' : item.status}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-eleball-text-secondary">{formatDate(item.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          <div className="flex items-center justify-between">
            <span className="text-sm text-eleball-text-secondary">共 {subTotal} 条记录</span>
            <div className="flex gap-2">
              <button
                onClick={() => fetchSubscriptions(subPage - 1)}
                disabled={subPage <= 1}
                className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-40"
              >
                上一页
              </button>
              <span className="px-3 py-1.5 rounded-lg bg-eleball-primary text-white text-sm font-medium">
                {subPage} / {totalSubPages}
              </span>
              <button
                onClick={() => fetchSubscriptions(subPage + 1)}
                disabled={subPage >= totalSubPages}
                className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-40"
              >
                下一页
              </button>
            </div>
          </div>
        </>
      )}
    </div>
  )
}
