import { useEffect, useState } from 'react'
import { moduleApi, clawMarketApi } from '../api/client'

export default function Modules() {
  const [modules, setModules] = useState([])
  const [drivers, setDrivers] = useState([])
  const [cloudInstalled, setCloudInstalled] = useState([]) // P4：云端已购可安装模块
  const [installing, setInstalling] = useState(null) // P4：安装中 module_id
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

  const [driverForm, setDriverForm] = useState({
    driver_id: '',
    name: '',
    description: '',
    transport_type: 'module',
    module_id: '',
    endpoint: '',
    auth_token: '',
    schema_json: ''
  })

  const fetchData = async () => {
    setLoading(true)
    setError('')
    try {
      const [mRes, dRes] = await Promise.all([moduleApi.listModules(), moduleApi.listDrivers()])
      setModules(mRes?.data?.items || mRes?.items || [])
      setDrivers(dRes?.data?.items || dRes?.items || [])
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

  // P4：安装云端已购模块到本地（官方预置直接激活；第三方拉镜像+签名校验）
  const handleInstall = async (meta) => {
    if (!window.confirm(`确定安装模块 ${meta.module_id} 到本地？${meta.official ? '（官方预置，直接激活）' : '（第三方，将拉取容器镜像并校验签名，需 Docker/Podman）'}`)) return
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

  // P4：本地秘技提交云端审核（转发云端 register，需 auth_token）
  const handleSubmitReview = async (m) => {
    const authToken = window.prompt(`提交模块 ${m.module_id} 到云端审核。\n请输入云端下发的 auth_token（管理员审批后获得）：`)
    if (authToken === null) return
    setError('')
    try {
      await moduleApi.submitForReview({
        module_id: m.module_id,
        name: m.name,
        description: m.description,
        url: m.url,
        transport_type: m.transport_type,
        capabilities: m.capabilities_list || [],
        version: m.version,
        auth_token: authToken,
      }, authToken)
      setError(`模块 ${m.module_id} 已提交云端审核`)
    } catch (err) {
      setError(err?.message || err || '提交失败')
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

  const handleDriverSubmit = async (e) => {
    e.preventDefault()
    setError('')
    try {
      await moduleApi.registerDriver(driverForm)
      setDriverForm({
        driver_id: '',
        name: '',
        description: '',
        transport_type: 'module',
        module_id: '',
        endpoint: '',
        auth_token: '',
        schema_json: ''
      })
      fetchData()
    } catch (err) {
      setError(err?.message || err || '提交失败')
    }
  }

  const handleDeleteModule = async (id) => {
    if (!window.confirm(`确定注销模块 ${id}？`)) return
    try {
      await moduleApi.deleteModule(id)
      fetchData()
    } catch (err) {
      setError(err?.message || err || '删除失败')
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

  const handleDeleteDriver = async (id) => {
    if (!window.confirm(`确定注销驱动 ${id}？`)) return
    try {
      await moduleApi.deleteDriver(id)
      fetchData()
    } catch (err) {
      setError(err?.message || err || '删除失败')
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
            onClick={() => setActiveTab('drivers')}
            className={`px-4 py-2 rounded-xl text-sm font-medium ${activeTab === 'drivers' ? 'bg-eleball-primary text-white' : 'bg-white border border-eleball-outline'}`}
          >
            驱动映射
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
              <thead className="bg-gray-50">
                <tr>
                  <th className="text-left px-4 py-3 font-medium">模块 ID</th>
                  <th className="text-left px-4 py-3 font-medium">名称</th>
                  <th className="text-left px-4 py-3 font-medium">传输类型</th>
                  <th className="text-left px-4 py-3 font-medium">状态</th>
                  <th className="text-left px-4 py-3 font-medium">版本</th>
                  <th className="text-left px-4 py-3 font-medium">最后心跳</th>
                  <th className="text-left px-4 py-3 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {modules.map((m) => (
                  <tr key={m.module_id} className="border-t border-eleball-outline">
                    <td className="px-4 py-3 font-mono">{m.module_id}</td>
                    <td className="px-4 py-3">{m.name}</td>
                    <td className="px-4 py-3">{m.transport_type}</td>
                    <td className="px-4 py-3">
                      <span className={`px-2 py-1 rounded-lg text-xs font-medium ${m.status === 'online' ? 'bg-green-100 text-green-700' : 'bg-gray-100 text-gray-600'}`}>
                        {m.status}
                      </span>
                      {m.error && (
                        <span className="ml-2 text-xs text-red-500 cursor-help" title={m.error}>!</span>
                      )}
                    </td>
                    <td className="px-4 py-3">{m.version || '-'}</td>
                    <td className="px-4 py-3">{formatTime(m.last_heartbeat)}</td>
                    <td className="px-4 py-3 space-x-2">
                      <button onClick={() => handleRefreshModule(m.module_id)} className="text-eleball-primary hover:underline">刷新</button>
                      <button onClick={() => handleSubmitReview(m)} className="text-blue-600 hover:underline">提交审核</button>
                      <button onClick={() => handleDeleteModule(m.module_id)} className="text-red-600 hover:underline">注销</button>
                    </td>
                  </tr>
                ))}
                {modules.length === 0 && !loading && (
                  <tr><td colSpan={7} className="px-4 py-8 text-center text-eleball-text-secondary">暂无模块</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}

      {activeTab === 'drivers' && (
        <>
          <div className="bg-white rounded-2xl border border-eleball-outline p-6">
            <h2 className="font-semibold mb-4">注册/更新驱动映射</h2>
            <form onSubmit={handleDriverSubmit} className="grid grid-cols-1 md:grid-cols-2 gap-4">
              <input required value={driverForm.driver_id} onChange={(e) => setDriverForm({ ...driverForm, driver_id: e.target.value })} placeholder="驱动 ID（如 firecrawl）" className="input" />
              <input required value={driverForm.name} onChange={(e) => setDriverForm({ ...driverForm, name: e.target.value })} placeholder="显示名称" className="input" />
              <select value={driverForm.transport_type} onChange={(e) => setDriverForm({ ...driverForm, transport_type: e.target.value })} className="input">
                <option value="module">module</option>
                <option value="remote_url">remote_url</option>
              </select>
              <input value={driverForm.module_id} onChange={(e) => setDriverForm({ ...driverForm, module_id: e.target.value })} placeholder="关联模块 ID（module 类型与 auth_token 二选一）" className="input" />
              <input value={driverForm.endpoint} onChange={(e) => setDriverForm({ ...driverForm, endpoint: e.target.value })} placeholder="Endpoint（remote_url 类型必填）" className="input" />
              <input value={driverForm.auth_token} onChange={(e) => setDriverForm({ ...driverForm, auth_token: e.target.value })} placeholder="自助注册令牌（module 类型与 module_id 二选一）" className="input" />
              <input value={driverForm.description} onChange={(e) => setDriverForm({ ...driverForm, description: e.target.value })} placeholder="描述" className="input" />
              <div className="md:col-span-2">
                <button type="submit" className="px-4 py-2 bg-eleball-primary text-white rounded-xl text-sm font-medium hover:bg-eleball-primary-dark transition-colors">
                  提交
                </button>
              </div>
            </form>
          </div>

          <div className="bg-white rounded-2xl border border-eleball-outline overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-gray-50">
                <tr>
                  <th className="text-left px-4 py-3 font-medium">驱动 ID</th>
                  <th className="text-left px-4 py-3 font-medium">名称</th>
                  <th className="text-left px-4 py-3 font-medium">传输类型</th>
                  <th className="text-left px-4 py-3 font-medium">关联模块 / Endpoint</th>
                  <th className="text-left px-4 py-3 font-medium">注册令牌</th>
                  <th className="text-left px-4 py-3 font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {drivers.map((d) => (
                  <tr key={d.driver_id} className="border-t border-eleball-outline">
                    <td className="px-4 py-3 font-mono">{d.driver_id}</td>
                    <td className="px-4 py-3">{d.name}</td>
                    <td className="px-4 py-3">{d.transport_type}</td>
                    <td className="px-4 py-3">{d.module_id || d.endpoint || '-'}</td>
                    <td className="px-4 py-3 font-mono">{d.auth_token || '-'}</td>
                    <td className="px-4 py-3">
                      <button onClick={() => handleDeleteDriver(d.driver_id)} className="text-red-600 hover:underline">注销</button>
                    </td>
                  </tr>
                ))}
                {drivers.length === 0 && !loading && (
                  <tr><td colSpan={6} className="px-4 py-8 text-center text-eleball-text-secondary">暂无驱动映射</td></tr>
                )}
              </tbody>
            </table>
          </div>
        </>
      )}

      {activeTab === 'cloud' && (
        <div className="bg-white rounded-2xl border border-eleball-outline overflow-hidden">
          <div className="px-4 py-3 border-b border-eleball-outline bg-gray-50">
            <h2 className="font-semibold text-sm">云端已购秘技</h2>
            <p className="text-xs text-eleball-text-secondary mt-1">
              从云端拉取已购模块，点「安装到本地」激活。官方模块直接激活，第三方模块拉取容器镜像并校验签名（需 Docker/Podman + cosign）。
            </p>
          </div>
          <table className="w-full text-sm">
            <thead className="bg-gray-50">
              <tr>
                <th className="text-left px-4 py-3 font-medium">模块</th>
                <th className="text-left px-4 py-3 font-medium">版本</th>
                <th className="text-left px-4 py-3 font-medium">类型</th>
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
                    {m.official ? (
                      <span className="px-2 py-0.5 rounded text-xs bg-emerald-50 text-emerald-600">官方预置</span>
                    ) : (
                      <span className="px-2 py-0.5 rounded text-xs bg-amber-50 text-amber-600">第三方</span>
                    )}
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
