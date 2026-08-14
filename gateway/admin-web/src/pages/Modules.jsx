import { useEffect, useMemo, useState } from 'react'
import { moduleApi, clawMarketApi, agentApi } from '../api/client'
import DockerMissingBanner from '../components/DockerMissingBanner'

// 模块来源标签（origin: builtin/cloud/user，T1.3 已去掉 eleball_ 前缀）
function originLabel(rt) {
  if (rt.origin === 'builtin') return 'eleball内置'
  if (rt.origin === 'cloud') return 'eleball云端'
  if (rt.origin === 'user') return rt.actor || '用户'
  return rt.official ? '官方' : '第三方'
}

// 状态机（T1.4 统一 status 枚举）
const STATUS_LABEL = {
  active: '在线', activating: '启动中', installed: '未运行',
  degraded: '异常', needs_update: '待更新', disabled: '已禁用',
}
const STATUS_BADGE = {
  active: 'bg-green-100 text-green-700',
  activating: 'bg-blue-100 text-blue-700',
  installed: 'bg-gray-100 text-gray-600',
  degraded: 'bg-red-100 text-red-700',
  needs_update: 'bg-amber-100 text-amber-700',
  disabled: 'bg-gray-100 text-gray-400',
}
// 包状态聚合优先级：在线 > 启动中 > 异常 > 待更新 > 未运行 > 禁用
const STATUS_ORDER = ['active', 'activating', 'degraded', 'needs_update', 'installed', 'disabled']

// 官方包判定（与后端 SkillRuntimeOrigin.IsOfficial 对齐）：builtin 恒官方，cloud 仅官方维护列表内官方。
const OFFICIAL_CLOUD = ['agent-reach', 'firecrawl', 'mcp-hello']
const isOfficialPkg = (origin, slug) =>
  origin === 'builtin' || (origin === 'cloud' && OFFICIAL_CLOUD.includes(slug))

// 包状态聚合：任一运行时在线即在线，否则按优先级降级。
function aggregateStatus(runtimes) {
  if (!runtimes || runtimes.length === 0) return 'installed'
  for (const s of STATUS_ORDER) if (runtimes.some((r) => r.status === s)) return s
  return 'installed'
}

// SKU 所属包 slug（manifest.metadata.package_module，回退 legacy 的 metadata.module，与 AgentMarket.packageKeyOf 对齐）
function skuPackageKey(sku) {
  try {
    const mf = typeof sku.manifest_json === 'string' ? JSON.parse(sku.manifest_json) : sku.manifest_json
    return mf?.metadata?.package_module || mf?.metadata?.module || ''
  } catch { return '' }
}

// 运行时能力清单（capabilities 为 JSON 字符串或数组）
function parseCaps(raw) {
  if (Array.isArray(raw)) return raw
  if (typeof raw === 'string' && raw) {
    try { const v = JSON.parse(raw); return Array.isArray(v) ? v : [] } catch { return [] }
  }
  return []
}

function StatusBadge({ status }) {
  const label = STATUS_LABEL[status] || status || '离线'
  const cls = STATUS_BADGE[status] || 'bg-gray-100 text-gray-600'
  return <span className={`px-2 py-1 rounded-lg text-xs font-medium ${cls}`}>{label}</span>
}

export default function Modules() {
  const [modules, setModules] = useState([]) // 运行时（skill_runtimes）
  const [skus, setSkus] = useState([]) // 秘技 SKU（AgentItem）
  const [cloudInstalled, setCloudInstalled] = useState([]) // 云端已购可安装模块
  const [installing, setInstalling] = useState(null)
  const [starting, setStarting] = useState(null)
  const [uninstalling, setUninstalling] = useState(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [activeTab, setActiveTab] = useState('local')

  const fetchData = async () => {
    setLoading(true)
    setError('')
    try {
      const [mRes, aRes] = await Promise.all([
        moduleApi.listModules().catch(() => null),
        agentApi.listAgents().catch(() => null),
      ])
      setModules(mRes?.items || mRes || [])
      const list = aRes?.items || aRes || []
      setSkus(Array.isArray(list) ? list : [])
      // 云端已购（失败不阻塞本地展示）
      clawMarketApi.listInstalledModules()
        .then((d) => setCloudInstalled(d?.items || d || []))
        .catch(() => setCloudInstalled([]))
    } catch (err) {
      setError(err?.message || err || '加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchData()
  }, [])

  // SKU 按包 slug 分组
  const skusByPackage = useMemo(() => {
    const m = new Map()
    for (const sku of skus) {
      const key = skuPackageKey(sku)
      if (!key) continue
      if (!m.has(key)) m.set(key, [])
      m.get(key).push(sku)
    }
    return m
  }, [skus])

  // 运行时按包（package_name 优先，空回退运行时 ID）分组 → 秘技包
  const packages = useMemo(() => {
    const m = new Map()
    for (const rt of modules) {
      const key = rt.package_name || rt.id
      let p = m.get(key)
      if (!p) {
        p = {
          key,
          title: rt.package_title || rt.name || key,
          description: rt.package_description || '',
          origin: rt.origin,
          actor: rt.actor,
          version: rt.version || '',
          runtimes: [],
        }
        m.set(key, p)
      }
      if (rt.package_title) p.title = rt.package_title
      else if (p.title === key && rt.name) p.title = rt.name
      if (rt.package_description) p.description = rt.package_description
      if (rt.version) p.version = rt.version
      p.runtimes.push(rt)
    }
    return [...m.values()]
  }, [modules])

  const handleStartModule = async (id) => {
    setStarting(id)
    setError('')
    try {
      await moduleApi.startModule(id)
      setError(`模块 ${id} 启动指令已发送`)
      setTimeout(() => fetchData(), 500)
      setTimeout(() => fetchData(), 3000)
    } catch (err) {
      setError(err?.message || err || '启动失败')
    } finally {
      setStarting(null)
    }
  }

  const handleRefreshModule = async (id) => {
    try {
      await moduleApi.refreshModule(id)
      fetchData()
    } catch (err) {
      setError(err?.message || err || '刷新失败')
    }
  }

  // 卸载整个秘技包（停进程/容器 + 删本地目录 + 下架 SKU + 注销运行时；官方包拒绝）
  const handleUninstall = async (pkg) => {
    const rt = pkg.runtimes[0]
    if (!rt) return
    if (!window.confirm(`确定卸载秘技包「${pkg.title}」？\n将停止运行并删除本地文件，不可恢复。`)) return
    setUninstalling(pkg.key)
    setError('')
    try {
      await moduleApi.uninstallModule(rt.id)
      setError(`已卸载「${pkg.title}」`)
      await fetchData()
    } catch (err) {
      setError(err?.message || err || '卸载失败')
    } finally {
      setUninstalling(null)
    }
  }

  // 分享本地秘技到云端审核（仅用户自制包）
  const handleSubmitReview = async (pkg) => {
    if (!window.confirm(`确定分享秘技包「${pkg.title}」到云端？\n提交后由管理员审核，通过后上架为云端秘技。`)) return
    setError('')
    try {
      await moduleApi.submitForReview(pkg.key)
      setError(`秘技包「${pkg.title}」已分享到云端，等待审核`)
    } catch (err) {
      setError(err?.message || err || '分享失败')
    }
  }

  // 安装云端已购模块到本地
  const handleInstall = async (meta) => {
    if (!window.confirm(`确定安装模块 ${meta.module_id} 到本地？（需 VIP1 及以上）${meta.official ? '（官方秘技，直接激活预置）' : '（第三方，将拉取容器镜像并校验签名，需 Docker/Podman）'}`)) return
    setInstalling(meta.module_id)
    setError('')
    try {
      await moduleApi.install(meta)
      setError(`模块 ${meta.module_id} 安装成功`)
      await fetchData()
    } catch (err) {
      setError(err?.message || err || '安装失败')
    } finally {
      setInstalling(null)
    }
  }

  const handleRescanMarketplace = async () => {
    if (!window.confirm('确定重新扫描 marketplace/ 目录？这会自动补齐新增的官方内置模块。')) return
    setLoading(true)
    setError('')
    try {
      await moduleApi.rescanMarketplace()
      await fetchData()
      setError('扫描完成')
    } catch (err) {
      setError(err?.message || err || '扫描失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">本地秘技</h1>
          <p className="text-sm text-eleball-text-secondary mt-1">
            秘技包与派生能力（SKU）的本地管理。官方包去「云端模块」更新，非官方包可卸载。
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => setActiveTab('local')}
            className={`px-4 py-2 rounded-xl text-sm font-medium ${activeTab === 'local' ? 'bg-eleball-primary text-white' : 'bg-white border border-eleball-outline'}`}
          >
            本地秘技包
          </button>
          <button
            onClick={() => setActiveTab('cloud')}
            className={`px-4 py-2 rounded-xl text-sm font-medium ${activeTab === 'cloud' ? 'bg-eleball-primary text-white' : 'bg-white border border-eleball-outline'}`}
          >
            云端模块
          </button>
          <button
            onClick={handleRescanMarketplace}
            disabled={loading}
            className="px-4 py-2 rounded-xl text-sm font-medium bg-eleball-primary text-white hover:bg-eleball-primary-dark disabled:opacity-50"
          >
            {loading ? '扫描中...' : '重新扫描'}
          </button>
        </div>
      </div>

      <DockerMissingBanner />

      {error && <div className="rounded-xl bg-red-50 text-red-600 px-4 py-3 text-sm">{error}</div>}

      {activeTab === 'local' && (
        <>
          {packages.length === 0 && !loading && (
            <div className="bg-white rounded-2xl border border-eleball-outline px-4 py-12 text-center text-eleball-text-secondary">
              暂无本地秘技。claw 启动时会扫描 marketplace/ 预置官方秘技包。
            </div>
          )}

          {packages.map((pkg) => {
            const official = isOfficialPkg(pkg.origin, pkg.key)
            const status = aggregateStatus(pkg.runtimes)
            const pkgSkus = skusByPackage.get(pkg.key) || []
            const activeSkus = pkgSkus.filter((s) => s.is_active).length
            return (
              <div key={pkg.key} className="bg-white rounded-2xl border border-eleball-outline overflow-hidden">
                {/* 包头 */}
                <div className="px-5 py-4 border-b border-eleball-outline bg-eleball-surface-variant/40">
                  <div className="flex items-start justify-between gap-4">
                    <div className="min-w-0">
                      <div className="flex items-center gap-2 flex-wrap">
                        <h2 className="font-semibold">{pkg.title}</h2>
                        <span className="font-mono text-xs text-eleball-text-secondary">{pkg.key}</span>
                        <span className={`px-2 py-0.5 rounded text-xs ${official ? 'bg-emerald-50 text-emerald-600' : 'bg-amber-50 text-amber-600'}`}>
                          {originLabel({ origin: pkg.origin, actor: pkg.actor, official })}
                        </span>
                        {official && <span className="px-2 py-0.5 rounded text-xs bg-emerald-50 text-emerald-600">官方</span>}
                      </div>
                      {pkg.description && (
                        <p className="text-xs text-eleball-text-secondary mt-1 line-clamp-2">{pkg.description}</p>
                      )}
                      <div className="flex items-center gap-3 text-xs text-eleball-text-tertiary mt-2">
                        <StatusBadge status={status} />
                        {pkg.version && <span>v{pkg.version}</span>}
                        <span>{pkg.runtimes.length} 个运行时</span>
                        <span>{pkgSkus.length} 个能力{activeSkus > 0 ? ` · ${activeSkus} 已激活` : ''}</span>
                      </div>
                    </div>
                    <div className="flex items-center gap-2 shrink-0">
                      {official ? (
                        <button
                          onClick={() => setActiveTab('cloud')}
                          className="px-3 py-1.5 rounded-lg text-xs font-medium border border-eleball-outline text-eleball-primary hover:bg-eleball-surface-variant"
                          title="官方包不可卸载，更新在「云端模块」"
                        >
                          云端更新
                        </button>
                      ) : (
                        <>
                          {pkg.origin === 'user' && (
                            <button
                              onClick={() => handleSubmitReview(pkg)}
                              className="px-3 py-1.5 rounded-lg text-xs font-medium border border-eleball-outline text-blue-600 hover:bg-blue-50"
                            >
                              分享到云端
                            </button>
                          )}
                          <button
                            onClick={() => handleUninstall(pkg)}
                            disabled={uninstalling === pkg.key}
                            className="px-3 py-1.5 rounded-lg text-xs font-medium border border-red-200 text-red-600 hover:bg-red-50 disabled:opacity-50"
                          >
                            {uninstalling === pkg.key ? '卸载中…' : '卸载'}
                          </button>
                        </>
                      )}
                    </div>
                  </div>
                </div>

                {/* 运行时子表 */}
                <table className="w-full text-sm">
                  <thead className="bg-eleball-surface-variant/40 text-eleball-text-secondary text-xs">
                    <tr>
                      <th className="text-left px-5 py-2 font-medium">运行时</th>
                      <th className="text-left px-4 py-2 font-medium">传输</th>
                      <th className="text-left px-4 py-2 font-medium">部署</th>
                      <th className="text-left px-4 py-2 font-medium">状态</th>
                      <th className="text-left px-4 py-2 font-medium">能力</th>
                      <th className="text-right px-5 py-2 font-medium">操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pkg.runtimes.map((rt) => {
                      const online = rt.status === 'active'
                      const canStart = !online && (rt.deployment === 'process' || rt.deployment === 'docker')
                      const caps = parseCaps(rt.capabilities)
                      return (
                        <tr key={rt.id} className="border-t border-eleball-outline">
                          <td className="px-5 py-2.5">
                            <div className="font-medium">{rt.name}</div>
                            <div className="text-xs text-eleball-text-secondary font-mono">{rt.id}</div>
                          </td>
                          <td className="px-4 py-2.5 text-xs text-eleball-text-secondary">{rt.transport || '-'}</td>
                          <td className="px-4 py-2.5 text-xs text-eleball-text-secondary">{rt.deployment || '-'}</td>
                          <td className="px-4 py-2.5"><StatusBadge status={rt.status} /></td>
                          <td className="px-4 py-2.5">
                            <div className="flex flex-wrap gap-1">
                              {caps.slice(0, 4).map((c) => (
                                <span key={c} className="text-[10px] px-1.5 py-0.5 rounded bg-gray-100 text-eleball-text-secondary">{c}</span>
                              ))}
                              {caps.length > 4 && <span className="text-[10px] text-eleball-text-secondary">+{caps.length - 4}</span>}
                              {caps.length === 0 && <span className="text-xs text-eleball-text-tertiary">-</span>}
                            </div>
                          </td>
                          <td className="px-5 py-2.5 text-right space-x-2">
                            {canStart && (
                              <button onClick={() => handleStartModule(rt.id)} disabled={starting === rt.id} className="text-emerald-600 hover:underline disabled:opacity-50">
                                {starting === rt.id ? '启动中…' : '启动'}
                              </button>
                            )}
                            <button onClick={() => handleRefreshModule(rt.id)} className="text-eleball-primary hover:underline">刷新</button>
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>

                {/* 派生 SKU（能力）清单 */}
                {pkgSkus.length > 0 && (
                  <div className="px-5 py-3 border-t border-eleball-outline bg-eleball-surface-variant/20">
                    <div className="text-xs font-medium text-eleball-text-secondary mb-2">派生能力（SKU）</div>
                    <div className="flex flex-wrap gap-2">
                      {pkgSkus.map((s) => (
                        <span
                          key={s.id}
                          className={`inline-flex items-center gap-1.5 px-2 py-1 rounded-lg text-xs border ${
                            s.is_active ? 'bg-emerald-50 border-emerald-200 text-emerald-700' : 'bg-white border-eleball-outline text-eleball-text-secondary'
                          }`}
                          title={s.description || s.id}
                        >
                          <span className={`w-1.5 h-1.5 rounded-full ${s.is_active ? 'bg-emerald-500' : 'bg-gray-300'}`} />
                          {s.name}
                        </span>
                      ))}
                    </div>
                  </div>
                )}
              </div>
            )
          })}
        </>
      )}

      {activeTab === 'cloud' && (
        <div className="bg-white rounded-2xl border border-eleball-outline overflow-hidden">
          <div className="px-4 py-3 border-b border-eleball-outline bg-eleball-surface-variant">
            <h2 className="font-semibold text-sm">云端已购秘技</h2>
            <p className="text-xs text-eleball-text-secondary mt-1">
              从云端拉取已购模块，点「安装到本地」激活。官方模块直接激活，第三方模块拉取容器镜像并校验签名（需 Docker/Podman + cosign）。
            </p>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-eleball-surface-variant">
              <tr>
                <th className="text-left px-4 py-3 font-medium">模块</th>
                <th className="text-left px-4 py-3 font-medium">版本</th>
                <th className="text-left px-4 py-3 font-medium">模块来源</th>
                <th className="text-left px-4 py-3 font-medium">镜像</th>
                <th className="text-left px-4 py-3 font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {cloudInstalled.map((m) => (
                <tr key={m.module_id} className="border-t border-eleball-outline">
                  <td className="px-4 py-3">
                    <div className="font-medium">{m.name}</div>
                    <div className="text-xs text-eleball-text-secondary font-mono">{m.module_id}</div>
                  </td>
                  <td className="px-4 py-3">{m.version || '-'}</td>
                  <td className="px-4 py-3">
                    <span className={`px-2 py-0.5 rounded text-xs ${m.official ? 'bg-emerald-50 text-emerald-600' : 'bg-amber-50 text-amber-600'}`}>
                      {m.official ? '官方' : originLabel({ origin: m.origin, actor: m.actor, official: m.official })}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-xs text-eleball-text-secondary font-mono">
                    {m.image ? `${m.image.repository}@${m.image.digest?.slice(0, 19) || m.image.tag || '-'}` : '-'}
                  </td>
                  <td className="px-4 py-3">
                    <button
                      onClick={() => handleInstall(m)}
                      disabled={installing === m.module_id}
                      className="px-3 py-1 rounded-lg text-xs font-medium bg-eleball-primary text-white hover:bg-eleball-primary-dark disabled:opacity-50"
                    >
                      {installing === m.module_id ? '安装中…' : '安装到本地'}
                    </button>
                  </td>
                </tr>
              ))}
              {cloudInstalled.length === 0 && !loading && (
                <tr><td colSpan={5} className="px-4 py-8 text-center text-eleball-text-secondary">
                  暂无云端已购秘技。请在云端 eleball.cn 购买秘技后刷新，或登录账号。
                </td></tr>
              )}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
