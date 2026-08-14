import { useEffect, useState } from 'react'
import { moduleApi, clawMarketApi } from '../api/client'
import DockerMissingBanner from '../components/DockerMissingBanner'

// 模块来源标签：origin 为 builtin/cloud/user（T1.3 字段改名，去掉 eleball_ 前缀）。
function sourceLabel(origin, actor, official) {
  if (origin === 'cloud') return 'eleball云端'
  if (origin === 'builtin') return 'eleball内置'
  if (origin === 'user') return actor || '用户'
  return official ? '官方' : '第三方'
}

// 模块状态标签（T1.4 统一状态机）。
const STATUS_LABEL = {
  active: '在线', activating: '启动中', installed: '未运行',
  degraded: '异常', needs_update: '待更新', disabled: '已禁用',
}

// 官方模块判定（与后端 SkillRuntimeOrigin.IsOfficial 对齐）：builtin 恒官方；
// cloud 仅官方维护列表内官方；user 一律非官方。
const OFFICIAL_CLOUD_MODULES = ['agent-reach', 'firecrawl', 'mcp-hello']
function isOfficialModule(m) {
  if (m.origin === 'builtin') return true
  if (m.origin === 'cloud') return OFFICIAL_CLOUD_MODULES.includes(m.package_name || m.id || '')
  return false
}

export default function Modules() {
  const [modules, setModules] = useState([])
  const [cloudInstalled, setCloudInstalled] = useState([]) // P4：云端已购可安装模块
  const [installing, setInstalling] = useState(null) // P4：安装中 module_id
  const [starting, setStarting] = useState(null) // 「启动服务」中的 module_id
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [activeTab, setActiveTab] = useState('modules')

  const [moduleForm, setModuleForm] = useState({
    module_id: '',
    name: '',
    description: '',
    url: '',
    transport_type: 'module',
    capabilities: '',
    version: '',
    auth_token: ''
  })

  const fetchData = async () => {
    setLoading(true)
    setError('')
    try {
      const mRes = await moduleApi.listModules()
      setModules(mRes?.data?.items || mRes?.items || [])
      // P4：拉取云端已购可安装模块（失败不阻塞本地展示）
      clawMarketApi.listInstalledModules()
        .then((d) => setCloudInstalled(d?.items || d || []))
        .catch(() => setCloudInstalled([]))
    } catch (err) {
      setError(err?.message || err || '加载失败')
    } finally {
      setLoading(false)
    }
  }

  // P4：安装云端已购模块到本地（均需 VIP1+；官方直接激活预置，第三方拉镜像+签名校验）
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

  // T8：本地秘技分享到云端审核（先提交、审核后下发；免 auth_token 鸡生蛋）
  const handleSubmitReview = async (m) => {
    if (!window.confirm(`确定分享模块 ${m.package_name || m.id} 到云端？\n提交后由管理员审核，通过后上架为云端秘技。`)) return
    setError('')
    try {
      await moduleApi.submitForReview(m.package_name || m.id)
      setError(`模块 ${m.package_name || m.id} 已分享到云端，等待审核`)
    } catch (err) {
      setError(err?.message || err || '分享失败')
    }
  }

  useEffect(() => {
    fetchData()
  }, [])

  const handleModuleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    try {
      const body = {
        ...moduleForm,
        capabilities: moduleForm.capabilities
          ? moduleForm.capabilities.split(',').map((s) => s.trim()).filter(Boolean)
          : []
      }
      await moduleApi.registerModule(body)
      setModuleForm({
        module_id: '',
        name: '',
        description: '',
        url: '',
        transport_type: 'module',
        capabilities: '',
        version: '',
        auth_token: ''
      })
      fetchData()
    } catch (err) {
      setError(err?.message || err || '提交失败')
    }
  }

  const handleUninstall = async (id) => {
    if (!window.confirm(`确定卸载模块 ${id}？\n将停止运行并删除本地文件，不可恢复。`)) return
    try {
      await moduleApi.uninstallModule(id)
      fetchData()
    } catch (err) {
      setError(err?.message || err || '卸载失败')
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

  // 拉起模块（process 同步 / docker 异步），「本地模块」页「启动服务」按钮调用。
  // 未激活模块不会自动启动，需先到集市激活；此处仅对已注册模块按部署方式拉起。
  const handleStartModule = async (id) => {
    setStarting(id)
    setError('')
    try {
      await moduleApi.startModule(id)
      setError(`模块 ${id} 启动指令已发送`)
      // process 同步起、docker 异步起（pull/compose 耗时）；分两次刷新覆盖两种情况。
      setTimeout(() => fetchData(), 500)
      setTimeout(() => fetchData(), 3000)
    } catch (err) {
      setError(err?.message || err || '启动失败')
    } finally {
      setStarting(null)
    }
  }

  const handleRescanMarketplace = async () => {
    if (!window.confirm('确定重新扫描 marketplace/ 目录？这会自动补齐新增的官方内置模块与驱动别名。')) return
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

  const formatTime = (t) => {
    if (!t) return '-'
    return new Date(t).toLocaleString('zh-CN')
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">集市模块 / 动态驱动</h1>
        <div className="flex gap-2">
          <button
            onClick={() => setActiveTab('modules')}
            className={`px-4 py-2 rounded-xl text-sm font-medium ${activeTab === 'modules' ? 'bg-eleball-primary text-white' : 'bg-white border border-eleball-outline'}`}
          >
            模块
          </button>
          <button
            onClick={() => setActiveTab('cloud')}
            className={`px-4 py-2 rounded-xl text-sm font-medium ${activeTab === 'cloud' ? 'bg-eleball-primary text-white' : 'bg-white border border-eleball-outline'}`}
          >
            云端已购
          </button>
        </div>
        <button
          onClick={handleRescanMarketplace}
          disabled={loading}
          className="px-4 py-2 rounded-xl text-sm font-medium bg-eleball-primary text-white hover:bg-eleball-primary-dark disabled:opacity-50"
        >
          {loading ? '扫描中...' : '扫描 Marketplace'}
        </button>
      </div>

      {/* Docker 缺失引导横幅：未安装 Docker 时提示安装指引，可关闭（存 localStorage） */}
      <DockerMissingBanner />

      {error && <div className="rounded-xl bg-red-50 text-red-600 px-4 py-3 text-sm">{error}</div>}

      {activeTab === 'modules' && (
        <>
          <div className="bg-white rounded-2xl border border-eleball-outline p-6">
            <h2 className="font-semibold mb-4">注册/更新模块</h2>
            <form onSubmit={handleModuleSubmit} className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <input value={moduleForm.module_id} onChange={(e) => setModuleForm({ ...moduleForm, module_id: e.target.value })} placeholder="模块 ID（留空自动生成）" className="input" />
              <input required value={moduleForm.name} onChange={(e) => setModuleForm({ ...moduleForm, name: e.target.value })} placeholder="显示名称" className="input" />
              <input required value={moduleForm.url} onChange={(e) => setModuleForm({ ...moduleForm, url: e.target.value })} placeholder="模块地址，如 http://firecrawl:8080" className="input" />
              <select value={moduleForm.transport_type} onChange={(e) => setModuleForm({ ...moduleForm, transport_type: e.target.value })} className="input">
                <option value="module">module</option>
                <option value="remote_url">remote_url</option>
              </select>
              <input value={moduleForm.capabilities} onChange={(e) => setModuleForm({ ...moduleForm, capabilities: e.target.value })} placeholder="能力清单，逗号分隔，如 scrape,crawl" className="input" />
              <input value={moduleForm.version} onChange={(e) => setModuleForm({ ...moduleForm, version: e.target.value })} placeholder="版本号" className="input" />
              <input value={moduleForm.auth_token} onChange={(e) => setModuleForm({ ...moduleForm, auth_token: e.target.value })} placeholder="自助注册令牌（可选）" className="input" />
              <input value={moduleForm.description} onChange={(e) => setModuleForm({ ...moduleForm, description: e.target.value })} placeholder="描述" className="input" />
              <div className="md:col-span-2">
                <button type="submit" className="px-4 py-2 bg-eleball-primary text-white rounded-xl text-sm font-medium hover:bg-eleball-primary-dark transition-colors">
                  提交
                </button>
              </div>
            </form>
          </div>

          <div className="bg-white rounded-2xl border border-eleball-outline overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-eleball-surface-variant">
                <tr>
                  <th className="text-left px-4 py-3 font-medium">模块 ID</th>
                  <th className="text-left px-4 py-3 font-medium">名称</th>
                  <th className="text-left px-4 py-3 font-medium">模块来源</th>
                  <th className="text-left px-4 py-3 font-medium">传输类型</th>
                  <th className="text-left px-4 py-3 font-medium">状态</th>
                  <th className="text-left px-4 py-3 font-medium">版本</th>
                  <th className="text-left px-4 py-3 font-medium">最后心跳</th>
                  <th className="text-left px-4 py-3 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {modules.map((m) => {
                  const online = m.status === 'active'
                  const canStart = !online && (m.deployment === 'process' || m.deployment === 'docker')
                  return (
                    <tr key={m.id} className="border-t border-eleball-outline">
                      <td className="px-4 py-3 font-mono">{m.id}</td>
                      <td className="px-4 py-3">{m.name}</td>
                      <td className="px-4 py-3 text-xs text-eleball-text-secondary">{sourceLabel(m.origin, m.actor, m.official) || '-'}</td>
                      <td className="px-4 py-3">{m.transport}</td>
                      <td className="px-4 py-3">
                        <span className={`px-2 py-1 rounded-lg text-xs font-medium ${online ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'}`}>
                          {online ? '在线' : (STATUS_LABEL[m.status] || m.status || '离线')}
                        </span>
                      </td>
                      <td className="px-4 py-3">{m.version || '-'}</td>
                      <td className="px-4 py-3">{formatTime(m.last_heartbeat)}</td>
                      <td className="px-4 py-3 space-x-2">
                        {canStart && (
                          <button
                            onClick={() => handleStartModule(m.id)}
                            disabled={starting === m.id}
                            className="text-emerald-600 hover:underline disabled:opacity-50"
                          >
                            {starting === m.id ? '启动中…' : '启动服务'}
                          </button>
                        )}
                        <button onClick={() => handleRefreshModule(m.id)} className="text-eleball-primary hover:underline">刷新</button>
                        <button onClick={() => handleSubmitReview(m)} className="text-blue-600 hover:underline">分享到云端</button>
                        {!isOfficialModule(m) && (
                          <button onClick={() => handleUninstall(m.id)} className="text-red-600 hover:underline">卸载</button>
                        )}
                      </td>
                    </tr>
                  )
                })}
                {modules.length === 0 && !loading && (
                  <tr><td colSpan={8} className="px-4 py-8 text-center text-eleball-text-secondary">暂无模块</td></tr>
                )}
              </tbody>
            </table>
          </div>
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
                    {(() => {
                      const label = sourceLabel(m.origin, m.actor, m.official)
                      const isEleball = m.origin === 'cloud' || m.origin === 'builtin' || (!m.origin && m.official)
                      return (
                        <span className={`px-2 py-0.5 rounded text-xs ${isEleball ? 'bg-emerald-50 text-emerald-600' : 'bg-amber-50 text-amber-600'}`}>
                          {label}
                        </span>
                      )
                    })()}
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
