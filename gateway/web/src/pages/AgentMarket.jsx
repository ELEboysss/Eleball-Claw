import { useState, useEffect, useMemo } from 'react'
import useSEO from '../hooks/useSEO'
import { useAuth } from '../context/AuthContext'
import { agentMarketApi, billingApi } from '../api/client'
import {
  Search,
  Star,
  Zap,
  Lock,
  Wallet,
  ShoppingCart,
  Heart,
  Loader2,
  Sparkles,
  Globe,
  Youtube,
  Github,
  BookOpen,
  MessageCircle,
  Settings,
  Cloud,
  CloudOff,
  AlertCircle
} from 'lucide-react'
import LoginModal from '../components/LoginModal'

const levelNames = {
  1: '黄阶秘技',
  2: '玄阶秘技',
  3: '地阶秘技',
  4: '天阶秘技',
  5: '仙阶秘技',
  6: '焚决'
}

const levelColors = {
  1: 'bg-stone-100 text-stone-600',
  2: 'bg-emerald-100 text-emerald-600',
  3: 'bg-blue-100 text-blue-600',
  4: 'bg-purple-100 text-purple-600',
  5: 'bg-amber-100 text-amber-600',
  6: 'bg-rose-100 text-rose-600'
}

const categoryIcons = {
  '互联网': Globe,
  '搜索': Globe,
  '开发': Github,
  '文件': BookOpen,
  '多媒体': Youtube,
  '系统': MessageCircle,
  '创意': Sparkles
}

export default function AgentMarket() {
  useSEO('Agent 技能市场', '可购买、可组合、可编排的 Agent 能力。给你的悬浮球加技能。')
  const { isLoggedIn, user } = useAuth()
  const [agents, setAgents] = useState([])
  const [categories, setCategories] = useState(['全部'])
  const [category, setCategory] = useState('全部')
  const [sort, setSort] = useState('hot')
  const [filter, setFilter] = useState('all')
  const [keyword, setKeyword] = useState('')
  const [loading, setLoading] = useState(true)
  const [capabilities, setCapabilities] = useState(null)
  const [balance, setBalance] = useState(null)
  const [message, setMessage] = useState('')
  const [loginOpen, setLoginOpen] = useState(false)
  const [purchasedIds, setPurchasedIds] = useState(new Set())
  const [credentialModal, setCredentialModal] = useState(null)
  const [credentialSchema, setCredentialSchema] = useState({})
  const [credentialValues, setCredentialValues] = useState({})
  const [credentialLoading, setCredentialLoading] = useState(false)
  const [credentialError, setCredentialError] = useState('')
  const [confirmAgent, setConfirmAgent] = useState(null)
  const [confirmLoading, setConfirmLoading] = useState(false)
  const [togglingId, setTogglingId] = useState(null)

  // 加载能力开关与余额
  useEffect(() => {
    if (!isLoggedIn) return
    agentMarketApi.getCapabilities().then(setCapabilities).catch(() => {})
    billingApi.getBalance().then(setBalance).catch(() => {})
    loadPurchasedAgents()
  }, [isLoggedIn])

  // 加载分类与秘技
  useEffect(() => {
    agentMarketApi.getCategories().then((d) => {
      const list = Array.isArray(d) ? d : []
      // 后端返回的分类本身不带“全部”，且可能重复；这里做去重和兜底
      const unique = ['全部', ...list.filter((c) => c && c !== '全部')]
      setCategories([...new Set(unique)])
    }).catch(() => {})
    loadAgents()
  }, [category, sort, filter])

  const loadAgents = () => {
    setLoading(true)
    setMessage('')
    agentMarketApi
      .listAgents(1, 50, category === '全部' ? '' : category, sort, filter === 'owned' ? 'owned' : '')
      .then((data) => {
        const items = data?.items || data || []
        setAgents(items)
      })
      .catch((err) => setMessage(err.message || '加载失败'))
      .finally(() => setLoading(false))
  }

  const loadPurchasedAgents = () => {
    agentMarketApi
      .getUserSpace()
      .then((space) => {
        const list = space?.purchased_agents || []
        setPurchasedIds(new Set(list.map((a) => a.id)))
      })
      .catch(() => {})
  }

  const filteredAgents = useMemo(() => {
    if (!keyword.trim()) return agents
    const k = keyword.toLowerCase()
    return agents.filter(
      (a) =>
        (a.name || '').toLowerCase().includes(k) ||
        (a.description || '').toLowerCase().includes(k) ||
        (a.category || '').toLowerCase().includes(k)
    )
  }, [agents, keyword])

  const parseManifestCredentials = (agent) => {
    try {
      const manifest = typeof agent.manifest_json === 'string' ? JSON.parse(agent.manifest_json) : agent.manifest_json
      return manifest?.credentials || {}
    } catch {
      return {}
    }
  }

  const openCredentialModal = async (agent) => {
    setCredentialError('')
    setCredentialValues({})
    setCredentialSchema({})
    setCredentialModal(agent)
    try {
      const data = await agentMarketApi.getCredentials(agent.id)
      setCredentialSchema(data?.credentials || {})
      setCredentialValues(data?.values || {})
    } catch (err) {
      setCredentialError(err.message || '加载凭证失败')
    }
  }

  const saveCredentials = async () => {
    if (!credentialModal) return
    setCredentialLoading(true)
    setCredentialError('')
    try {
      await agentMarketApi.saveCredentials(credentialModal.id, credentialValues)
      setMessage(`${credentialModal.name} 凭证已保存`)
      setCredentialModal(null)
    } catch (err) {
      setCredentialError(err.message || '凭证保存失败')
    } finally {
      setCredentialLoading(false)
    }
  }

  const openPurchaseConfirm = (agent) => {
    if (!isLoggedIn) {
      setLoginOpen(true)
      return
    }
    setConfirmAgent(agent)
  }

  const handlePurchase = async (currency = 'danwan') => {
    if (!confirmAgent) return
    setConfirmLoading(true)
    setMessage('')
    try {
      await agentMarketApi.purchase(confirmAgent.id, currency)
      setPurchasedIds((prev) => new Set([...prev, confirmAgent.id]))
      setMessage(`购买成功：${confirmAgent.name}`)
      setConfirmAgent(null)
      loadAgents()
      billingApi.getBalance().then(setBalance).catch(() => {})
    } catch (err) {
      setMessage(err.message || '购买失败')
    } finally {
      setConfirmLoading(false)
    }
  }

  const handleToggleActive = async (agent) => {
    if (!isLoggedIn) {
      setLoginOpen(true)
      return
    }
    setTogglingId(agent.id)
    setMessage('')
    try {
      const res = await agentMarketApi.toggleActive(agent.id)
      const active = res?.active ?? !agent.is_active
      setAgents((prev) =>
        prev.map((a) =>
          a.id === agent.id
            ? {
                ...a,
                is_active: active,
                active_count: active
                  ? (a.active_count || 0) + 1
                  : Math.max(0, (a.active_count || 0) - 1)
              }
            : a
        )
      )
      setMessage(`${agent.name} 已${active ? '激活' : '取消激活'}`)
    } catch (err) {
      setMessage(err.message || '切换激活状态失败')
    } finally {
      setTogglingId(null)
    }
  }

  // 未登录引导
  if (!isLoggedIn) {
    return (
      <div className="pt-24 px-4 text-center min-h-screen">
        <div className="max-w-md mx-auto card">
          <ShoppingCart className="w-12 h-12 mx-auto mb-4 text-eleball-primary" />
          <h2 className="text-xl font-bold text-eleball-text mb-2">登录后探索秘技集市</h2>
          <p className="text-sm text-eleball-text-secondary mb-6">
            发现并购买社区精选 SubAgent，按需解锁 Agent-Reach 等高级能力。
          </p>
          <button onClick={() => setLoginOpen(true)} className="btn-primary w-full justify-center">
            登录 / 注册
          </button>
        </div>
        <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />
      </div>
    )
  }

  // 市场未开放
  if (capabilities && !capabilities.agent_market?.enabled) {
    return (
      <div className="pt-24 px-4 text-center min-h-screen">
        <div className="max-w-md mx-auto card">
          <Lock className="w-12 h-12 text-eleball-text-tertiary mx-auto mb-4" />
          <h2 className="text-xl font-bold text-eleball-text">秘技集市暂未开放</h2>
          <p className="text-sm text-eleball-text-secondary mt-2">
            当前仅对管理员开放，敬请期待。
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="pt-8 pb-16 px-4 max-w-6xl mx-auto min-h-screen">
      {/* 标题区 */}
      <div className="text-center mb-6">
        <h1 className="text-2xl font-bold text-eleball-text mb-2">{filter === 'owned' ? '我的秘技' : '秘技集市'}</h1>
        <p className="text-sm text-eleball-text-secondary">
          {filter === 'owned' ? '你已购买和激活的秘技' : 'agent模式下可使用的skills及MCP工具'}
        </p>
      </div>

      {/* 全部 / 我的秘技 Tab */}
      <div className="flex justify-center mb-6">
        <div className="inline-flex p-1 rounded-xl bg-eleball-surface-variant border border-eleball-outline-variant">
          <button
            onClick={() => setFilter('all')}
            className={`px-5 py-2 rounded-lg text-sm font-medium transition-all ${
              filter === 'all'
                ? 'bg-white text-eleball-primary shadow-sm'
                : 'text-eleball-text-secondary hover:text-eleball-text'
            }`}
          >
            全部秘技
          </button>
          <button
            onClick={() => setFilter('owned')}
            className={`px-5 py-2 rounded-lg text-sm font-medium transition-all ${
              filter === 'owned'
                ? 'bg-white text-eleball-primary shadow-sm'
                : 'text-eleball-text-secondary hover:text-eleball-text'
            }`}
          >
            我的秘技
          </button>
        </div>
      </div>

      {/* 余额与消息 */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 mb-6">
        <div className="flex items-center gap-3 text-sm text-eleball-text-secondary">
          <div className="flex items-center gap-2">
            <Wallet className="w-4 h-4" />
            <span>余额：</span>
            <span className="font-semibold text-eleball-text">
              {(balance?.danwan ?? 0).toLocaleString('zh-CN')} 弹丸
            </span>
            {balance?.elegant > 0 && (
              <span className="text-eleball-text-tertiary">
                / {(balance.elegant).toLocaleString('zh-CN')} 优雅弹丸
              </span>
            )}
          </div>

        </div>
        {message && (
          <div className={`text-sm px-4 py-2 rounded-2xl ${message.includes('成功') ? 'bg-emerald-50 text-emerald-600' : message.includes('激活') ? 'bg-blue-50 text-blue-600' : 'bg-red-50 text-red-600'}`}>
            {message}
          </div>
        )}
      </div>

      {/* 筛选、排序、搜索 */}
      <div className="flex flex-col md:flex-row md:items-center gap-4 mb-6">
        <div className="flex flex-wrap gap-2">
          {categories.map((c) => (
            <button
              key={c}
              onClick={() => setCategory(c)}
              className={`px-3 py-1.5 rounded-full text-xs font-medium transition-colors ${
                category === c
                  ? 'bg-eleball-primary text-white'
                  : 'bg-eleball-surface-variant text-eleball-text-secondary hover:bg-eleball-primary-light hover:text-eleball-primary'
              }`}
            >
              {c}
            </button>
          ))}
        </div>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value)}
          className="input text-sm py-1.5 md:w-32"
        >
          <option value="hot">最热</option>
          <option value="new">最新</option>
          <option value="rating">评分</option>
        </select>
        <div className="relative flex-1 md:max-w-xs ml-auto">
          <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-eleball-text-tertiary" />
          <input
            type="text"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜索秘技..."
            className="input w-full pl-9 py-2 text-sm"
          />
        </div>
      </div>

      {/* 加载与空态 */}
      {loading && (
        <div className="flex justify-center py-12">
          <Loader2 className="w-8 h-8 animate-spin text-eleball-primary" />
        </div>
      )}
      {!loading && filteredAgents.length === 0 && (
        <div className="text-center py-16 text-eleball-text-secondary">
          <Zap className="w-12 h-12 mx-auto mb-4 opacity-40" />
          <p>{filter === 'owned' ? '你还没有购买任何秘技，去集市看看吧' : '暂无符合条件的秘技'}</p>
          {filter === 'owned' && (
            <button
              onClick={() => setFilter('all')}
              className="mt-4 text-eleball-primary hover:underline text-sm"
            >
              前往秘技集市
            </button>
          )}
        </div>
      )}

      {/* 卡片网格 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        {filteredAgents.map((agent) => {
          const Icon = categoryIcons[agent.category] || Sparkles
          const isPurchased = purchasedIds.has(agent.id)
          return (
            <div
              key={agent.id}
              className="card p-5 flex flex-col hover:border-eleball-primary/40 transition-colors"
            >
              <div className="flex items-start justify-between mb-4">
                <div className="w-12 h-12 rounded-2xl bg-eleball-primary-light flex items-center justify-center shrink-0">
                  <Icon className="w-6 h-6 text-eleball-primary" />
                </div>
                <div className="flex items-center gap-2">
                  {agent.driver_registered === false && (
                    <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-orange-50 text-orange-600 flex items-center gap-1" title="该秘技依赖的驱动别名尚未注册，暂不可购买/领取">
                      <AlertCircle className="w-3 h-3" />
                      未注册
                    </span>
                  )}
                  {agent.driver_registered !== false && agent.module_online === false && (
                    <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-red-50 text-red-600 flex items-center gap-1" title="该秘技依赖的模块当前离线，激活后不会进入工具表">
                      <CloudOff className="w-3 h-3" />
                      离线
                    </span>
                  )}
                  {agent.driver_registered !== false && agent.module_online === true && (
                    <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-emerald-50 text-emerald-600 flex items-center gap-1" title="模块在线，可正常调用">
                      <Cloud className="w-3 h-3" />
                      在线
                    </span>
                  )}
                  <span
                    className={`text-xs px-2 py-0.5 rounded-full font-medium ${
                      levelColors[agent.level] || levelColors[1]
                    }`}
                  >
                    {levelNames[agent.level] || '秘技'}
                  </span>
                  {Object.keys(parseManifestCredentials(agent)).length > 0 && (
                    <button
                      onClick={() => openCredentialModal(agent)}
                      className="text-eleball-text-tertiary hover:text-eleball-primary transition-colors"
                      title="配置凭证"
                    >
                      <Settings className="w-4 h-4" />
                    </button>
                  )}
                  <button
                    onClick={() => {}}
                    className="text-eleball-text-tertiary hover:text-eleball-primary transition-colors"
                    title="收藏"
                  >
                    <Heart className="w-4 h-4" />
                  </button>
                </div>
              </div>

              <h3 className="font-semibold text-eleball-text mb-1">{agent.name}</h3>
              <p className="text-xs text-eleball-text-secondary line-clamp-2 mb-3">
                {agent.description}
              </p>

              <div className="flex items-center gap-1 text-xs text-eleball-text-tertiary mb-4">
                <Star className="w-3 h-3" />
                <span>{agent.avg_rating ? agent.avg_rating.toFixed(1) : '—'}</span>
                <span className="mx-1">·</span>
                <span>{agent.active_count || 0} 人激活</span>
                <span className="mx-1">·</span>
                <span>{agent.creator_name || '官方'}</span>
              </div>

              <div className="mt-auto flex items-center justify-between gap-3">
                <div className="text-sm">
                  {agent.price_danwan === 0 ? (
                    <span className="text-emerald-600 font-medium">免费</span>
                  ) : (
                    <span className="font-bold text-eleball-text">
                      {agent.price_danwan.toLocaleString('zh-CN')} 弹丸
                    </span>
                  )}
                  {agent.price_elegant && (
                    <span className="text-xs text-eleball-text-secondary ml-1 block">
                      或 {agent.price_elegant.toLocaleString('zh-CN')} 优雅弹丸
                    </span>
                  )}
                </div>
                {isPurchased && agent.manifest_json ? (
                  <label className={`inline-flex items-center gap-2 px-3 py-2 rounded-full text-xs font-medium transition-colors ${agent.driver_registered === false ? 'cursor-not-allowed bg-gray-100 text-gray-400' : 'cursor-pointer ' + (agent.is_active ? 'bg-emerald-50 text-emerald-600' : 'bg-eleball-surface-variant text-eleball-text-secondary')}`}>
                    <input
                      type="checkbox"
                      className="sr-only"
                      checked={!!agent.is_active}
                      disabled={agent.driver_registered === false || togglingId === agent.id}
                      onChange={() => handleToggleActive(agent)}
                    />
                    <span className={`relative inline-flex h-4 w-7 items-center rounded-full transition-colors ${agent.is_active ? 'bg-emerald-500' : 'bg-gray-300'}`}>
                      <span className={`inline-block h-2.5 w-2.5 transform rounded-full bg-white transition-transform ${agent.is_active ? 'translate-x-3.5' : 'translate-x-1'}`} />
                    </span>
                    {agent.driver_registered === false
                      ? '驱动未注册'
                      : togglingId === agent.id
                        ? '处理中...'
                        : agent.is_active
                          ? agent.module_online === false
                            ? '已激活（离线）'
                            : '已激活'
                          : '未激活'}
                  </label>
                ) : isPurchased ? (
                  <span className="px-4 py-2 rounded-full text-sm font-medium bg-eleball-surface-variant text-eleball-text-secondary">
                    已拥有
                  </span>
                ) : agent.driver_registered === false ? (
                  <button
                    disabled
                    className="text-sm px-4 py-2 rounded-full font-medium bg-gray-100 text-gray-400 cursor-not-allowed"
                    title="驱动未注册，暂不可购买"
                  >
                    {agent.price_danwan === 0 ? '暂不可领' : '不可购买'}
                  </button>
                ) : (
                  <button
                    onClick={() => openPurchaseConfirm(agent)}
                    className="btn-primary text-sm px-4 py-2"
                  >
                    {agent.price_danwan === 0 ? '免费领取' : '购买'}
                  </button>
                )}
              </div>
            </div>
          )
        })}
      </div>

      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />

      {/* 购买确认弹窗 */}
      {confirmAgent && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-2xl w-full max-w-sm">
            <div className="p-4 border-b border-eleball-outline flex items-center justify-between">
              <h3 className="font-bold text-eleball-text">确认购买</h3>
              <button
                onClick={() => setConfirmAgent(null)}
                className="text-eleball-text-tertiary hover:text-eleball-text"
                disabled={confirmLoading}
              >
                &times;
              </button>
            </div>
            <div className="p-4 space-y-3">
              <p className="text-sm text-eleball-text-secondary">
                确认{confirmAgent.price_danwan === 0 ? '领取' : '购买'}
                <span className="font-medium text-eleball-text mx-1">{confirmAgent.name}</span>
                吗？
              </p>
              <div className="text-sm text-eleball-text-secondary">
                价格：
                <span className="font-semibold text-eleball-text">
                  {confirmAgent.price_danwan === 0
                    ? '免费'
                    : `${confirmAgent.price_danwan.toLocaleString('zh-CN')} 弹丸`}
                </span>
              </div>
              {confirmAgent.module_online === false && (
                <div className="text-xs px-3 py-2 rounded-xl bg-red-50 text-red-600">
                  注意：该秘技依赖的模块当前离线，购买后即使激活也不会载入工具，待模块恢复后自动生效。
                </div>
              )}
              <div className="flex gap-3 pt-2">
                <button
                  onClick={() => setConfirmAgent(null)}
                  disabled={confirmLoading}
                  className="flex-1 px-4 py-2 rounded-xl text-sm font-medium border border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors disabled:opacity-50"
                >
                  取消
                </button>
                <button
                  onClick={() => handlePurchase('danwan')}
                  disabled={confirmLoading}
                  className="flex-1 btn-primary text-sm py-2 justify-center disabled:opacity-50"
                >
                  {confirmLoading ? '处理中...' : confirmAgent.price_danwan === 0 ? '确认领取' : '确认购买'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* SKU 凭证配置弹窗 */}
      {credentialModal && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-2xl w-full max-w-md max-h-[80vh] overflow-auto">
            <div className="p-4 border-b border-eleball-outline flex items-center justify-between">
              <h3 className="font-bold text-eleball-text">{credentialModal.name} 凭证配置</h3>
              <button onClick={() => setCredentialModal(null)} className="text-eleball-text-tertiary hover:text-eleball-text">
                &times;
              </button>
            </div>
            <div className="p-4 space-y-4">
              {credentialError && (
                <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">
                  {credentialError}
                </div>
              )}
              {Object.keys(credentialSchema).length === 0 && !credentialError && (
                <div className="text-sm text-eleball-text-secondary text-center py-4">加载中...</div>
              )}
              {Object.entries(credentialSchema).map(([key, def]) => (
                <div key={key} className="border border-eleball-outline rounded-xl p-3 space-y-2">
                  <div className="flex items-center justify-between">
                    <span className="font-medium text-eleball-text">{def.label || key}</span>
                    {def.required && <span className="text-xs text-red-500">必填</span>}
                  </div>
                  {def.description && (
                    <p className="text-xs text-eleball-text-secondary">{def.description}</p>
                  )}
                  <textarea
                    value={credentialValues[key] || ''}
                    onChange={(e) => setCredentialValues((prev) => ({ ...prev, [key]: e.target.value }))}
                    placeholder={def.placeholder || `填写 ${def.label || key}`}
                    className="input w-full text-xs h-20 resize-none"
                  />
                </div>
              ))}
              <button
                onClick={saveCredentials}
                disabled={credentialLoading}
                className="btn-primary text-sm px-4 py-2 w-full justify-center disabled:opacity-50"
              >
                {credentialLoading ? '保存中...' : '保存凭证'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
