import { useState, useEffect, useMemo } from 'react'
import { useSearchParams, Link } from 'react-router-dom'
import useSEO from '../hooks/useSEO'
import { useAuth } from '../context/AuthContext'
import { agentMarketApi, billingApi, clawMarketApi, moduleGeneratorApi } from '../api/client'
import PageHero from '../components/PageHero'
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
  CloudDownload,
  AlertCircle,
  Wand2,
  Package
} from 'lucide-react'
import LoginModal from '../components/LoginModal'
import DockerMissingBanner from '../components/DockerMissingBanner'
import AssistantManager from '../components/AssistantManager'

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

// 模块来源标签：eleball_cloud->eleball云端 / eleball_builtin->eleball内置 / user·mcp->主体名；
// source_origin 缺失（旧云端）回退 official 合成「官方/第三方」。
function sourceLabel(origin, actor, official) {
  if (origin === 'eleball_cloud') return 'eleball云端'
  if (origin === 'eleball_builtin') return 'eleball内置'
  if (origin === 'user' || origin === 'mcp') return actor || (origin === 'mcp' ? 'MCP' : '用户')
  return official ? '官方' : '第三方'
}

export default function AgentMarket() {
  useSEO('Agent 技能市场', '可购买、可组合、可编排的 Agent 能力。给你的悬浮球加技能。')
  const { isLoggedIn, user } = useAuth()
  // Tab：all=全部秘技 / owned=我的秘技 / assistants=我的助手；支持 ?tab=assistants 直接定位（对话页「管理助手」入口）
  const [searchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') === 'assistants' ? 'assistants' : 'all'
  const [agents, setAgents] = useState([])
  const [categories, setCategories] = useState(['全部'])
  const [category, setCategory] = useState('全部')
  const [sort, setSort] = useState('hot')
  const [filter, setFilter] = useState(initialTab)
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
  // 云端已购秘技（ModuleInstallMeta 列表）与安装中 module_id
  const [cloudMetas, setCloudMetas] = useState([])
  const [installingId, setInstallingId] = useState(null)
  // H2：依赖管理弹窗（模块级，stdio+process 模块的 requirements.txt/package.json）
  const [depsModal, setDepsModal] = useState(null) // agent 卡片对象
  const [depsStatus, setDepsStatus] = useState(null)
  const [depsLoading, setDepsLoading] = useState(false)
  const [depsInstalling, setDepsInstalling] = useState(false)
  const [depsError, setDepsError] = useState('')
  // C1：卡片详情大窗 + 评论区懒加载
  const [detailAgent, setDetailAgent] = useState(null)
  const [detailReviews, setDetailReviews] = useState([])
  const [detailReviewsLoading, setDetailReviewsLoading] = useState(false)

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

  // C1：打开详情大窗时懒加载评论区（仅本地秘技；云端已购未安装卡片无评价接口，跳过）
  useEffect(() => {
    if (!detailAgent || detailAgent.cloud_not_installed) {
      setDetailReviews([])
      return
    }
    setDetailReviewsLoading(true)
    agentMarketApi
      .listReviews(detailAgent.id)
      .then((d) => setDetailReviews(d?.items || d || []))
      .catch(() => setDetailReviews([]))
      .finally(() => setDetailReviewsLoading(false))
  }, [detailAgent])

  const loadAgents = () => {
    // 「我的助手」Tab 不加载秘技列表，助手数据由 AssistantManager 自行加载
    if (filter === 'assistants') return
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
    // 并行拉取云端已购秘技（未登录/失败静默降级，只显示本地）
    loadCloudMetas()
  }

  // 拉取云端已购秘技元数据，用于合并展示「云端已购·未安装」卡片
  const loadCloudMetas = () => {
    if (!isLoggedIn) {
      setCloudMetas([])
      return
    }
    clawMarketApi
      .listInstalledModules()
      .then((d) => setCloudMetas(d?.items || d || []))
      .catch(() => {})
  }

  const loadPurchasedAgents = () => {
    // 云端已购（/space）+ 本地免费获取（本地 filter=owned）合并判定「已购」
    agentMarketApi
      .getUserSpace()
      .then((space) => {
        const list = space?.purchased_agents || []
        setPurchasedIds((prev) => new Set([...prev, ...list.map((a) => a.id)]))
      })
      .catch(() => {})
    agentMarketApi
      .listAgents(1, 100, '', 'hot', 'owned')
      .then((data) => {
        const items = data?.items || data || []
        setPurchasedIds((prev) => new Set([...prev, ...items.map((a) => a.id)]))
      })
      .catch(() => {})
  }

  // 解析本地秘技 manifest 中声明的模块 ID（用于与云端已购列表去重）
  const parseManifestModuleId = (agent) => {
    try {
      const manifest = typeof agent.manifest_json === 'string' ? JSON.parse(agent.manifest_json) : agent.manifest_json
      return manifest?.metadata?.module || ''
    } catch {
      return ''
    }
  }

  // C1：解析本地秘技 manifest 全量对象（详情大窗展示用）；云端卡片无 manifest_json 返回 null
  const parseManifest = (agent) => {
    try {
      const manifest = typeof agent.manifest_json === 'string' ? JSON.parse(agent.manifest_json) : agent.manifest_json
      return manifest || null
    } catch {
      return null
    }
  }

  // 把云端 ModuleInstallMeta 合成为与本地卡片同构的展示对象（卡片数据来自 meta + meta.manifest）
  const metaToCloudCard = (meta) => {
    let manifest = meta.manifest
    if (typeof manifest === 'string') {
      try {
        manifest = JSON.parse(manifest)
      } catch {
        manifest = null
      }
    }
    return {
      id: meta.agent_id || meta.module_id,
      name: manifest?.name || meta.name || meta.module_id,
      description: manifest?.description || meta.description || '',
      category: manifest?.category || '',
      level: manifest?.level || 1,
      price_danwan: 0,
      // 云端下发的统计数据（评分/购买数/激活数），缺失时兜底 0
      avg_rating: meta.avg_rating ?? 0,
      active_count: meta.active_count ?? 0,
      purchase_count: meta.purchase_count ?? 0,
      creator_name: sourceLabel(meta.source_origin, meta.source_actor, meta.official),
      // 云端已购·未安装标记，渲染「下载到本地」按钮
      cloud_not_installed: true,
      cloud_meta: meta
    }
  }

  // 本地列表 + 云端已购未安装合并：本地已有同 agent_id 或同 module_id 的秘技时去重
  const displayAgents = useMemo(() => {
    const localIds = new Set(agents.map((a) => a.id))
    const localModuleIds = new Set(agents.map((a) => parseManifestModuleId(a)).filter(Boolean))
    const cloudCards = cloudMetas
      .filter((m) => {
        const aid = m.agent_id || m.module_id
        if (localIds.has(aid)) return false
        if (m.module_id && localModuleIds.has(m.module_id)) return false
        return true
      })
      .map(metaToCloudCard)
      .filter((c) => category === '全部' || !c.category || c.category === category)
    return [...agents, ...cloudCards]
  }, [agents, cloudMetas, category])

  const filteredAgents = useMemo(() => {
    if (!keyword.trim()) return displayAgents
    const k = keyword.toLowerCase()
    return displayAgents.filter(
      (a) =>
        (a.name || '').toLowerCase().includes(k) ||
        (a.description || '').toLowerCase().includes(k) ||
        (a.category || '').toLowerCase().includes(k)
    )
  }, [displayAgents, keyword])

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
      if (confirmAgent.cloud_not_installed) {
        // 云端卡片：走云端购买（云端账户扣费）
        await agentMarketApi.purchase(confirmAgent.id, currency)
        setPurchasedIds((prev) => new Set([...prev, confirmAgent.id]))
        setMessage(`购买成功：${confirmAgent.name}，可下载到本地安装`)
      } else {
        // 本地卡片：走本地购买（仅免费 SKU 会成功，付费 SKU 由后端返回提示）
        await agentMarketApi.purchaseLocal(confirmAgent.id, currency)
        setPurchasedIds((prev) => new Set([...prev, confirmAgent.id]))
        setMessage(`领取成功：${confirmAgent.name}，可在卡片上激活使用`)
      }
      setConfirmAgent(null)
      loadAgents()
      loadCloudMetas()
      billingApi.getBalance().then(setBalance).catch(() => {})
    } catch (err) {
      setMessage(err.message || '购买失败')
    } finally {
      setConfirmLoading(false)
    }
  }

  // 下载/安装云端已购秘技到本地：凡云端来源（无论 official）均需 VIP1+；第三方另需 Docker/Podman
  const handleInstall = async (meta) => {
    const isVip = (user?.vip_level ?? 0) >= 1 || user?.role === 'admin'
    let hint = meta.official
      ? '（官方秘技，安装后直接激活，需 VIP1 及以上）'
      : '（第三方秘技，将拉取容器镜像并校验签名，需 Docker/Podman，且需 VIP1 及以上）'
    // 前置提示升级 VIP，但不阻断（后端 4002 兜底）
    if (!isVip) {
      hint += '\n检测到当前账号未开通 VIP，安装可能被拒绝，建议先升级 VIP。'
    }
    if (!window.confirm(`确定下载并安装「${meta.name || meta.module_id}」到本地？${hint}`)) return
    setInstallingId(meta.module_id)
    setMessage('')
    try {
      await clawMarketApi.installModule(meta)
      setMessage(`${meta.name || meta.module_id} 安装成功，可在卡片上激活使用`)
      loadAgents()
    } catch (err) {
      if (err.code === 4002) {
        setMessage(`${err.message || '该云端秘技需 VIP1 及以上'}，请升级 VIP 后重试`)
      } else {
        setMessage(err.message || '安装失败')
      }
    } finally {
      setInstallingId(null)
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
      // 云端来源秘技激活的 VIP1+ 门禁（code=4002）：展示升级引导
      if (err.code === 4002) {
        setMessage(`${err.message || '该云端秘技需 VIP1 及以上'}，请升级 VIP 后重试`)
      } else {
        setMessage(err.message || '切换激活状态失败')
      }
    } finally {
      setTogglingId(null)
    }
  }

  // H2：打开依赖管理弹窗，拉取模块依赖状态（包列表 + 安装状态）
  const openDepsModal = async (agent) => {
    setDepsModal(agent)
    setDepsStatus(null)
    setDepsError('')
    setDepsLoading(true)
    const moduleID = parseManifestModuleId(agent)
    if (!moduleID) {
      setDepsError('无法解析模块 ID')
      setDepsLoading(false)
      return
    }
    try {
      const data = await moduleGeneratorApi.depsStatus(moduleID)
      setDepsStatus(data)
    } catch (err) {
      setDepsError(err.message || '加载依赖状态失败')
    } finally {
      setDepsLoading(false)
    }
  }

  // H2：安装模块依赖（python venv / node npm），用户显式触发
  const handleInstallDeps = async () => {
    if (!depsModal) return
    const moduleID = parseManifestModuleId(depsModal)
    if (!moduleID) return
    setDepsInstalling(true)
    setDepsError('')
    try {
      const res = await moduleGeneratorApi.installDeps(moduleID)
      setDepsStatus(res)
      setMessage(`${depsModal.name} 依赖安装完成`)
      // 安装会重启模块，刷新列表以更新在线/依赖状态
      loadAgents()
    } catch (err) {
      setDepsError(err.message || '安装依赖失败')
    } finally {
      setDepsInstalling(false)
    }
  }

  // C1：卡片与详情大窗共用的底部动作区（价格 + 购买/激活/下载）。
  // 内部交互元素一律 stopPropagation，使卡片 onClick 打开详情不被按钮点击误触；
  // 在详情大窗内调用时无父级 onClick，stopPropagation 为无副作用空操作。
  const renderAgentActions = (agent) => {
    const isPurchased = purchasedIds.has(agent.id)
    return (
      <div className="mt-auto flex items-center justify-between gap-3">
        <div className="text-sm">
          {agent.cloud_not_installed ? (
            <span className="text-sky-600 font-medium">云端已购</span>
          ) : agent.price_danwan === 0 ? (
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
        {agent.cloud_not_installed ? (
          <button
            onClick={(e) => { e.stopPropagation(); handleInstall(agent.cloud_meta) }}
            disabled={installingId === agent.cloud_meta?.module_id}
            className="btn-primary text-sm px-4 py-2 flex items-center gap-1.5 disabled:opacity-50"
            title={agent.cloud_meta?.official ? '官方秘技，需 VIP1 及以上' : '第三方秘技，需 Docker/Podman 且需 VIP1 及以上'}
          >
            {installingId === agent.cloud_meta?.module_id ? (
              <>
                <Loader2 className="w-3.5 h-3.5 animate-spin" />
                安装中...
              </>
            ) : (
              <>
                <CloudDownload className="w-3.5 h-3.5" />
                下载到本地
              </>
            )}
          </button>
        ) : isPurchased && agent.manifest_json ? (
          <label
            onClick={(e) => e.stopPropagation()}
            className={`inline-flex items-center gap-2 px-3 py-2 rounded-full text-xs font-medium transition-colors ${agent.driver_registered === false || agent.credential_complete === false ? 'cursor-not-allowed bg-gray-100 text-gray-400' : 'cursor-pointer ' + (agent.is_active ? 'bg-emerald-50 text-emerald-600' : 'bg-eleball-surface-variant text-eleball-text-secondary')}`}
          >
            <input
              type="checkbox"
              className="sr-only"
              checked={!!agent.is_active}
              disabled={agent.driver_registered === false || agent.credential_complete === false || togglingId === agent.id}
              onChange={() => handleToggleActive(agent)}
            />
            <span className={`relative inline-flex h-4 w-7 items-center rounded-full transition-colors ${agent.is_active ? 'bg-emerald-500' : 'bg-gray-300'}`}>
              <span className={`inline-block h-2.5 w-2.5 transform rounded-full bg-white transition-transform ${agent.is_active ? 'translate-x-3.5' : 'translate-x-1'}`} />
            </span>
            {agent.driver_registered === false
              ? '驱动未注册'
              : agent.credential_complete === false
                ? '凭证不全'
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
            onClick={(e) => e.stopPropagation()}
            disabled
            className="text-sm px-4 py-2 rounded-full font-medium bg-gray-100 text-gray-400 cursor-not-allowed"
            title="驱动未注册，暂不可购买"
          >
            {agent.price_danwan === 0 ? '暂不可领' : '不可购买'}
          </button>
        ) : (
          <button
            onClick={(e) => { e.stopPropagation(); openPurchaseConfirm(agent) }}
            className="btn-primary text-sm px-4 py-2"
          >
            {agent.price_danwan === 0 ? '免费领取' : '购买'}
          </button>
        )}
      </div>
    )
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
        <PageHero
          align="center"
          title={filter === 'owned' ? '我的秘技' : filter === 'assistants' ? '我的助手' : '秘技集市'}
          subtitle={
            filter === 'owned'
              ? '你已购买和激活的秘技'
              : filter === 'assistants'
                ? '助手是已激活秘技的命名组合，在对话页绑定后仅载入组合内的工具'
                : 'agent 模式下可使用的 skills 及 MCP 工具'
          }
        />
        <Link
          to="/studio"
          className="inline-flex items-center gap-1.5 mt-3 text-xs font-medium text-eleball-primary hover:underline"
        >
          <Wand2 className="w-3.5 h-3.5" /> 自己造一个秘技 →
        </Link>
      </div>

      {/* Docker 缺失引导横幅：未安装 Docker 时提示安装指引，可关闭（存 localStorage） */}
      <DockerMissingBanner />

      {/* claw 未登录提示：仅展示本地自部署模块与驱动，登录后可拉取云端已购秘技 */}
      {!isLoggedIn && (
        <div className="mb-6 rounded-xl border border-eleball-primary/30 bg-eleball-primary-light/50 px-4 py-3 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <p className="text-sm text-eleball-text-secondary">
            当前仅展示本地自部署模块与驱动。若需要更多秘技，请登录账号从云端拉取已购。
          </p>
          <button
            onClick={() => setLoginOpen(true)}
            className="btn-primary text-xs px-4 py-1.5 shrink-0"
          >
            登录账号
          </button>
        </div>
      )}

      {/* 全部 / 我的秘技 / 我的助手 Tab */}
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
          <button
            onClick={() => setFilter('assistants')}
            className={`px-5 py-2 rounded-lg text-sm font-medium transition-all ${
              filter === 'assistants'
                ? 'bg-white text-eleball-primary shadow-sm'
                : 'text-eleball-text-secondary hover:text-eleball-text'
            }`}
          >
            我的助手
          </button>
        </div>
      </div>

      {/* 我的助手 Tab：助手管理（CRUD + 秘技多选） */}
      {filter === 'assistants' && <AssistantManager />}

      {filter !== 'assistants' && (
      <>
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
          return (
            <div
              key={agent.id}
              onClick={() => setDetailAgent(agent)}
              className="card p-5 flex flex-col hover:border-eleball-primary/40 transition-colors cursor-pointer"
            >
              <div className="flex items-start justify-between mb-4">
                <div className="w-12 h-12 rounded-2xl bg-eleball-primary-light flex items-center justify-center shrink-0">
                  <Icon className="w-6 h-6 text-eleball-primary" />
                </div>
                <div className="flex items-center gap-2">
                  {agent.cloud_not_installed && (
                    <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-sky-50 text-sky-600 flex items-center gap-1" title="云端已购，下载安装到本地后可激活使用">
                      <CloudDownload className="w-3 h-3" />
                      云端已购·未安装
                    </span>
                  )}
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
                  {agent.credential_complete === false && (
                    <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-amber-50 text-amber-600 flex items-center gap-1" title="该秘技需配置凭证才能激活，点击齿轮图标配置">
                      <AlertCircle className="w-3 h-3" />
                      凭证不全
                    </span>
                  )}
                  {agent.has_deps && !agent.deps_installed && (
                    <button
                      onClick={(e) => { e.stopPropagation(); openDepsModal(agent) }}
                      className="text-xs px-2 py-0.5 rounded-full font-medium bg-orange-50 text-orange-600 flex items-center gap-1 hover:bg-orange-100 transition-colors"
                      title="该秘技依赖第三方包未安装，点击安装"
                    >
                      <Package className="w-3 h-3" />
                      依赖未装
                    </button>
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
                      onClick={(e) => { e.stopPropagation(); openCredentialModal(agent) }}
                      className="text-eleball-text-tertiary hover:text-eleball-primary transition-colors"
                      title="配置凭证"
                    >
                      <Settings className="w-4 h-4" />
                    </button>
                  )}
                  <button
                    onClick={(e) => e.stopPropagation()}
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
                <span>{agent.avg_rating ? agent.avg_rating.toFixed(1) : '-'}</span>
                <span className="mx-1">·</span>
                <span>{agent.active_count || 0} 人激活</span>
                <span className="mx-1">·</span>
                <span>{agent.creator_name || '官方'}</span>
              </div>

              {renderAgentActions(agent)}
            </div>
          )
        })}
      </div>
      </>
      )}

      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />

      {/* 购买确认弹窗 */}
      {confirmAgent && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="dialog-panel w-full max-w-sm">
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
          <div className="dialog-panel w-full max-w-md max-h-[80vh] overflow-auto">
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
                    <span className="font-medium text-eleball-text flex items-center gap-1.5">
                      {def.label || key}
                      {def.scope === 'module' && (
                        <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-blue-50 text-blue-600 font-normal" title="此凭证在同模块所有 SKU 间共享，配置一次即可">
                          模块级共享
                        </span>
                      )}
                    </span>
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

      {/* H2：依赖管理弹窗（模块级，展示包列表 + 风险提示 + 安装按钮） */}
      {depsModal && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="dialog-panel w-full max-w-md max-h-[80vh] overflow-auto">
            <div className="p-4 border-b border-eleball-outline flex items-center justify-between">
              <h3 className="font-bold text-eleball-text flex items-center gap-1.5">
                <Package className="w-4 h-4" />
                {depsModal.name} 依赖管理
              </h3>
              <button onClick={() => setDepsModal(null)} className="text-eleball-text-tertiary hover:text-eleball-text">
                &times;
              </button>
            </div>
            <div className="p-4 space-y-3">
              {depsError && (
                <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{depsError}</div>
              )}
              {depsLoading && (
                <div className="text-sm text-eleball-text-secondary text-center py-4">加载中...</div>
              )}
              {depsStatus && !depsLoading && (
                <>
                  {!depsStatus.has_deps ? (
                    <div className="text-sm text-eleball-text-secondary text-center py-4">
                      该模块无第三方依赖。
                    </div>
                  ) : (
                    <>
                      <div className="flex items-center gap-2 text-xs">
                        <span className="px-2 py-0.5 rounded-full bg-eleball-surface-variant text-eleball-text-secondary font-medium">
                          {depsStatus.type === 'node' ? 'Node.js' : 'Python'}
                        </span>
                        <span className={`px-2 py-0.5 rounded-full font-medium ${depsStatus.installed ? 'bg-emerald-50 text-emerald-600' : 'bg-orange-50 text-orange-600'}`}>
                          {depsStatus.installed ? '已安装' : '未安装'}
                        </span>
                      </div>
                      <div className="text-xs px-3 py-2 rounded-xl bg-amber-50 text-amber-700 leading-relaxed">
                        ⚠️ 将安装以下第三方依赖。依赖来自模块的 {depsStatus.type === 'node' ? 'package.json' : 'requirements.txt'}，由模块作者声明。请确认来源可信后再安装。
                      </div>
                      <div className="space-y-1.5 max-h-48 overflow-auto">
                        {(depsStatus.packages || []).map((p, i) => (
                          <div key={i} className="flex items-center justify-between text-xs px-3 py-1.5 rounded-lg bg-eleball-surface-variant">
                            <span className="font-mono text-eleball-text">{p.name}</span>
                            {p.spec && <span className="font-mono text-eleball-text-tertiary">{p.spec}</span>}
                          </div>
                        ))}
                        {(!depsStatus.packages || depsStatus.packages.length === 0) && (
                          <div className="text-xs text-eleball-text-secondary text-center py-2">无包声明</div>
                        )}
                      </div>
                      {depsStatus.log && (
                        <details className="text-xs">
                          <summary className="text-eleball-text-tertiary cursor-pointer">安装输出</summary>
                          <pre className="mt-1 px-3 py-2 rounded-lg bg-gray-50 text-eleball-text-secondary overflow-auto max-h-32 whitespace-pre-wrap">{depsStatus.log}</pre>
                        </details>
                      )}
                      <button
                        onClick={handleInstallDeps}
                        disabled={depsInstalling}
                        className="btn-primary text-sm px-4 py-2 w-full justify-center disabled:opacity-50"
                      >
                        {depsInstalling ? (
                          <span className="flex items-center justify-center gap-1.5">
                            <Loader2 className="w-3.5 h-3.5 animate-spin" /> 安装中…
                          </span>
                        ) : depsStatus.installed ? (
                          '重新安装'
                        ) : (
                          '安装依赖'
                        )}
                      </button>
                    </>
                  )}
                </>
              )}
            </div>
          </div>
        </div>
      )}

      {/* C1：秘技详情大窗（点击卡片打开，展示完整描述 / manifest 详情 / 系统提示词 / 统计 / 评价） */}
      {detailAgent && (() => {
        const agent = detailAgent
        const Icon = categoryIcons[agent.category] || Sparkles
        const manifest = parseManifest(agent)
        const creds = manifest?.credentials || {}
        const hasCreds = Object.keys(creds).length > 0
        return (
          <div
            className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4"
            onClick={() => setDetailAgent(null)}
          >
            <div
              className="dialog-panel w-full max-w-2xl max-h-[85vh] flex flex-col"
              onClick={(e) => e.stopPropagation()}
            >
              {/* 头部：图标 + 名称 + 等级 + 状态徽章 + 关闭 */}
              <div className="p-4 border-b border-eleball-outline flex items-start gap-3">
                <div className="w-12 h-12 rounded-2xl bg-eleball-primary-light flex items-center justify-center shrink-0 overflow-hidden">
                  {agent.icon_url ? (
                    <img src={agent.icon_url} alt={agent.name} className="w-full h-full object-cover" />
                  ) : (
                    <Icon className="w-6 h-6 text-eleball-primary" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <h3 className="font-bold text-eleball-text truncate">{agent.name}</h3>
                    <span className={`text-xs px-2 py-0.5 rounded-full font-medium ${levelColors[agent.level] || levelColors[1]}`}>
                      {levelNames[agent.level] || '秘技'}
                    </span>
                  </div>
                  <div className="flex items-center gap-1.5 flex-wrap mt-1.5">
                    {agent.cloud_not_installed && (
                      <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-sky-50 text-sky-600 flex items-center gap-1">
                        <CloudDownload className="w-3 h-3" /> 云端已购·未安装
                      </span>
                    )}
                    {agent.driver_registered === false && (
                      <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-orange-50 text-orange-600 flex items-center gap-1">
                        <AlertCircle className="w-3 h-3" /> 未注册
                      </span>
                    )}
                    {agent.driver_registered !== false && agent.module_online === false && (
                      <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-red-50 text-red-600 flex items-center gap-1">
                        <CloudOff className="w-3 h-3" /> 离线
                      </span>
                    )}
                    {agent.driver_registered !== false && agent.module_online === true && (
                      <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-emerald-50 text-emerald-600 flex items-center gap-1">
                        <Cloud className="w-3 h-3" /> 在线
                      </span>
                    )}
                    {agent.credential_complete === false && (
                      <span className="text-xs px-2 py-0.5 rounded-full font-medium bg-amber-50 text-amber-600 flex items-center gap-1">
                        <AlertCircle className="w-3 h-3" /> 凭证不全
                      </span>
                    )}
                  </div>
                </div>
                <button onClick={() => setDetailAgent(null)} className="text-eleball-text-tertiary hover:text-eleball-text shrink-0 text-xl leading-none">
                  &times;
                </button>
              </div>

              {/* 内容区：可滚动 */}
              <div className="p-4 space-y-4 overflow-auto">
                {/* 完整描述（不截断） */}
                <p className="text-sm text-eleball-text-secondary leading-relaxed whitespace-pre-wrap">
                  {agent.description || '暂无描述'}
                </p>

                {/* 统计 */}
                <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 text-center">
                  <div className="rounded-xl bg-eleball-surface-variant p-2">
                    <div className="text-sm font-bold text-eleball-text flex items-center justify-center gap-1">
                      <Star className="w-3 h-3" />{agent.avg_rating ? agent.avg_rating.toFixed(1) : '-'}
                    </div>
                    <div className="text-[10px] text-eleball-text-tertiary mt-0.5">评分</div>
                  </div>
                  <div className="rounded-xl bg-eleball-surface-variant p-2">
                    <div className="text-sm font-bold text-eleball-text">{agent.active_count || 0}</div>
                    <div className="text-[10px] text-eleball-text-tertiary mt-0.5">人激活</div>
                  </div>
                  <div className="rounded-xl bg-eleball-surface-variant p-2">
                    <div className="text-sm font-bold text-eleball-text">{agent.purchase_count || 0}</div>
                    <div className="text-[10px] text-eleball-text-tertiary mt-0.5">人购买</div>
                  </div>
                  <div className="rounded-xl bg-eleball-surface-variant p-2">
                    <div className="text-sm font-bold text-eleball-text">{agent.use_count || 0}</div>
                    <div className="text-[10px] text-eleball-text-tertiary mt-0.5">次使用</div>
                  </div>
                </div>
                <div className="flex items-center gap-2 flex-wrap text-xs text-eleball-text-tertiary">
                  <span>作者：{agent.creator_name || '官方'}</span>
                  {agent.category && <span>· 分类：{agent.category}</span>}
                  {agent.favorite_count != null && <span>· {agent.favorite_count} 人收藏</span>}
                </div>

                {/* manifest 能力详情（本地秘技；云端卡片无 manifest 跳过） */}
                {manifest && (
                  <div className="space-y-3">
                    <div className="text-xs font-semibold text-eleball-text-tertiary uppercase tracking-wide">能力详情</div>
                    <div className="grid grid-cols-2 gap-2 text-xs">
                      {manifest.driver && (
                        <div className="flex items-center gap-1.5"><span className="text-eleball-text-tertiary">驱动</span><span className="font-mono text-eleball-text">{manifest.driver}</span></div>
                      )}
                      {manifest.runtime_type && (
                        <div className="flex items-center gap-1.5"><span className="text-eleball-text-tertiary">运行时</span><span className="font-mono text-eleball-text">{manifest.runtime_type}</span></div>
                      )}
                      {manifest.timeout_seconds > 0 && (
                        <div className="flex items-center gap-1.5"><span className="text-eleball-text-tertiary">超时</span><span className="text-eleball-text">{manifest.timeout_seconds}s</span></div>
                      )}
                      {manifest.pricing && manifest.pricing.unit && (
                        <div className="flex items-center gap-1.5"><span className="text-eleball-text-tertiary">计费</span><span className="text-eleball-text">{manifest.pricing.unit} / {manifest.pricing.currency || 'danwan'}</span></div>
                      )}
                    </div>

                    {/* 权限 */}
                    {manifest.permissions && manifest.permissions.length > 0 && (
                      <div className="flex items-center gap-1.5 flex-wrap text-xs">
                        <span className="text-eleball-text-tertiary">权限：</span>
                        {manifest.permissions.map((p) => (
                          <span key={p} className="px-1.5 py-0.5 rounded-full bg-blue-50 text-blue-600 font-medium">{p}</span>
                        ))}
                      </div>
                    )}

                    {/* 支持动作 */}
                    {manifest.actions && manifest.actions.length > 0 && (
                      <div className="space-y-1.5">
                        <div className="text-eleball-text-tertiary text-xs">支持动作</div>
                        {manifest.actions.map((a, i) => (
                          <div key={i} className="rounded-lg bg-eleball-surface-variant p-2 text-xs">
                            <div className="font-mono text-eleball-text">{a.name}</div>
                            {a.description && <div className="text-eleball-text-secondary mt-0.5">{a.description}</div>}
                          </div>
                        ))}
                      </div>
                    )}

                    {/* M5：伪工具的资源/提示清单（read_resource/get_prompt 伪工具暴露的可读资源/可用提示，只读） */}
                    {manifest.metadata?.pseudo_tool === 'resource' && (
                      <div className="space-y-1.5">
                        <div className="text-eleball-text-tertiary text-xs">可读资源</div>
                        {(manifest.parameters?.properties?.uri?.enum || []).map((uri) => (
                          <div key={uri} className="rounded-lg bg-eleball-surface-variant p-2 text-xs font-mono text-eleball-text break-all">{uri}</div>
                        ))}
                        {(!manifest.parameters?.properties?.uri?.enum || manifest.parameters.properties.uri.enum.length === 0) && (
                          <div className="text-xs text-eleball-text-secondary">该 server 暂无可读资源</div>
                        )}
                      </div>
                    )}
                    {manifest.metadata?.pseudo_tool === 'prompt' && (
                      <div className="space-y-1.5">
                        <div className="text-eleball-text-tertiary text-xs">可用提示</div>
                        {(manifest.parameters?.properties?.name?.enum || []).map((name) => (
                          <div key={name} className="rounded-lg bg-eleball-surface-variant p-2 text-xs font-mono text-eleball-text">{name}</div>
                        ))}
                        {(!manifest.parameters?.properties?.name?.enum || manifest.parameters.properties.name.enum.length === 0) && (
                          <div className="text-xs text-eleball-text-secondary">该 server 暂无可用提示</div>
                        )}
                      </div>
                    )}

                    {/* 参数 schema（折叠） */}
                    {manifest.parameters && Object.keys(manifest.parameters).length > 0 && (
                      <details className="text-xs">
                        <summary className="text-eleball-text-tertiary cursor-pointer">参数 schema</summary>
                        <pre className="mt-1 px-3 py-2 rounded-lg bg-gray-50 text-eleball-text-secondary overflow-auto max-h-48 text-[11px] whitespace-pre-wrap">{JSON.stringify(manifest.parameters, null, 2)}</pre>
                      </details>
                    )}

                    {/* 所需凭证 */}
                    {hasCreds && (
                      <div className="space-y-1.5">
                        <div className="text-eleball-text-tertiary text-xs">所需凭证</div>
                        {Object.entries(creds).map(([key, def]) => (
                          <div key={key} className="rounded-lg bg-eleball-surface-variant p-2 text-xs">
                            <div className="flex items-center gap-1.5 flex-wrap">
                              <span className="font-medium text-eleball-text">{def.label || key}</span>
                              <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-gray-100 text-gray-500">{def.type}</span>
                              {def.scope === 'module' && <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-blue-50 text-blue-600">模块级共享</span>}
                              {def.required && <span className="text-red-500 text-[10px]">必填</span>}
                            </div>
                            {def.description && <div className="text-eleball-text-secondary mt-0.5">{def.description}</div>}
                          </div>
                        ))}
                      </div>
                    )}

                    {/* 模块元数据（折叠） */}
                    {manifest.metadata && Object.keys(manifest.metadata).length > 0 && (
                      <details className="text-xs">
                        <summary className="text-eleball-text-tertiary cursor-pointer">模块元数据</summary>
                        <pre className="mt-1 px-3 py-2 rounded-lg bg-gray-50 text-eleball-text-secondary overflow-auto max-h-32 text-[11px] whitespace-pre-wrap">{JSON.stringify(manifest.metadata, null, 2)}</pre>
                      </details>
                    )}
                  </div>
                )}

                {/* 系统提示词（折叠） */}
                {agent.system_prompt && (
                  <details className="text-xs">
                    <summary className="text-eleball-text-tertiary cursor-pointer">系统提示词</summary>
                    <pre className="mt-1 px-3 py-2 rounded-lg bg-gray-50 text-eleball-text-secondary overflow-auto max-h-48 whitespace-pre-wrap">{agent.system_prompt}</pre>
                  </details>
                )}

                {/* 用户评价（仅本地秘技；云端已购未安装卡片无评价接口） */}
                {!agent.cloud_not_installed && (
                  <div className="space-y-2">
                    <div className="text-xs font-semibold text-eleball-text-tertiary uppercase tracking-wide">用户评价</div>
                    {detailReviewsLoading && <div className="text-sm text-eleball-text-secondary text-center py-2">加载中...</div>}
                    {!detailReviewsLoading && detailReviews.length === 0 && (
                      <div className="text-sm text-eleball-text-secondary text-center py-2">暂无评价</div>
                    )}
                    {detailReviews.map((r) => (
                      <div key={r.id} className="rounded-lg bg-eleball-surface-variant p-2 text-xs">
                        <div className="flex items-center gap-1.5 mb-0.5">
                          <span className="font-medium text-eleball-text">{r.user_name || '匿名'}</span>
                          <span className="text-eleball-primary flex items-center gap-0.5">
                            {Array.from({ length: r.rating || 0 }).map((_, i) => <Star key={i} className="w-2.5 h-2.5 fill-current" />)}
                          </span>
                        </div>
                        {r.comment && <div className="text-eleball-text-secondary">{r.comment}</div>}
                      </div>
                    ))}
                  </div>
                )}
              </div>

              {/* 底部动作区：凭证 / 依赖 + 价格与主操作（与卡片共用 renderAgentActions） */}
              <div className="p-4 border-t border-eleball-outline">
                {(hasCreds || (agent.has_deps && !agent.deps_installed)) && (
                  <div className="flex items-center gap-2 mb-3">
                    {hasCreds && (
                      <button
                        onClick={() => openCredentialModal(agent)}
                        className="text-xs px-3 py-1.5 rounded-full border border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors flex items-center gap-1.5"
                      >
                        <Settings className="w-3.5 h-3.5" /> 配置凭证
                      </button>
                    )}
                    {agent.has_deps && !agent.deps_installed && (
                      <button
                        onClick={() => openDepsModal(agent)}
                        className="text-xs px-3 py-1.5 rounded-full border border-eleball-outline text-orange-600 hover:bg-orange-50 transition-colors flex items-center gap-1.5"
                      >
                        <Package className="w-3.5 h-3.5" /> 安装依赖
                      </button>
                    )}
                  </div>
                )}
                {renderAgentActions(agent)}
              </div>
            </div>
          </div>
        )
      })()}
    </div>
  )
}
