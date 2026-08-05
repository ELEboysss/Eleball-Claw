import { useEffect, useMemo, useState } from 'react'
import { eleAgentModelApi } from '../api/client'

/**
 * claw 模型配置（本地 CRUD，双通道）。
 *   - Ele Agent 云端代理：一键添加，Base URL 指向 api.eleball.cn，API Key 用当前登录态，
 *     模型列表从云端公开接口拉取；调用经 gateway 网关代理，由云端账户计费（本地不计费）。
 *   - BYOK：添加自有模型（任意 OpenAI 兼容/厂商协议端点），本地不计费，不展示价格字段。
 * PR-C3：由扁平表格改为「按 provider 分组的开关式卡片」（对齐 cloud admin-web C2 视觉）。
 */

const PROTOCOLS = [
  { value: 'openai_compatible', label: 'OpenAI 兼容' },
  { value: 'anthropic_messages', label: 'Anthropic Messages' },
  { value: 'gemini_generative', label: 'Gemini Generative' },
  { value: 'seedream', label: 'Seedream（即梦图片）' },
  { value: 'seedance', label: 'Seedance（火山视频）' },
  { value: 'agnes_image', label: 'Agnes Image' },
  { value: 'agnes_video', label: 'Agnes Video' },
]

const CAPABILITY_FIELDS = [
  { key: 'supports_chat', label: '对话' },
  { key: 'supports_vision', label: '视觉理解' },
  { key: 'supports_image', label: '图片生成' },
  { key: 'supports_video', label: '视频生成' },
  { key: 'supports_image_input', label: '图片输入' },
  { key: 'supports_tools', label: '工具调用' },
]

const protocolLabel = (p) => {
  switch (p) {
    case 'anthropic_messages': return 'Anthropic'
    case 'gemini_generative': return 'Gemini'
    case 'agnes_image': return 'Agnes Image'
    case 'agnes_video': return 'Agnes Video'
    case 'seedance': return 'Seedance'
    case 'seedream': return 'Seedream'
    default: return 'OpenAI 兼容'
  }
}

// 开关组件：紫底圆角药丸 + 滑动圆点（对齐 cloud admin-web C2）
function Toggle({ checked, onChange, disabled }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => !disabled && onChange(!checked)}
      className={`relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors duration-200 focus:outline-none focus:ring-2 focus:ring-eleball-primary/30 ${
        checked ? 'bg-eleball-primary' : 'bg-gray-300'
      } ${disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'}`}
    >
      <span className={`inline-block h-5 w-5 transform rounded-full bg-white shadow transition-transform duration-200 ${checked ? 'translate-x-5' : 'translate-x-0.5'}`} />
    </button>
  )
}

function CapBadge({ label, cls }) {
  return (
    <span className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${cls}`}>{label}</span>
  )
}

// 是否云端 Ele Agent 代理（经 gateway 网关，base_url 指向 api.eleball.cn）
const isCloudProxy = (item) => (item.base_url || '').includes('api.eleball.cn')

export default function EleAgentModels() {
  const [items, setItems] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [total, setTotal] = useState(0)
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState(null) // 编辑中的配置（null=新建）
  const [isProxyPreset, setIsProxyPreset] = useState(false) // Ele Agent 云端代理快捷表单
  const [form, setForm] = useState(emptyForm)
  const [submitting, setSubmitting] = useState(false)
  const [cloudOptions, setCloudOptions] = useState([]) // 云端可选模型（快捷添加时选 model_name）

  const fetchItems = async () => {
    setLoading(true)
    setError('')
    try {
      // 卡片按 provider 分组，需拉取较全量
      const data = await eleAgentModelApi.list(1, 500)
      const list = Array.isArray(data?.items) ? data.items : []
      setItems(list)
      setTotal(data?.total ?? list.length)
    } catch (err) {
      setError(typeof err === 'string' ? err : (err?.message || '加载失败'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchItems()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 按 provider 分组，组内按 priority 升序
  const grouped = useMemo(() => {
    const map = {}
    for (const it of items) {
      const key = it.provider || '(未分组)'
      if (!map[key]) map[key] = []
      map[key].push(it)
    }
    return Object.entries(map)
      .map(([provider, list]) => ({
        provider,
        list: list.slice().sort((a, b) => (a.priority || 0) - (b.priority || 0)),
      }))
      .sort((a, b) => a.provider.localeCompare(b.provider))
  }, [items])

  // 一键开关：乐观更新 + 失败回滚
  const handleToggle = async (item, next) => {
    setItems((prev) => prev.map((i) => (i.id === item.id ? { ...i, is_enabled: next } : i)))
    try {
      await eleAgentModelApi.update(item.id, { is_enabled: next })
    } catch (err) {
      setItems((prev) => prev.map((i) => (i.id === item.id ? { ...i, is_enabled: item.is_enabled } : i)))
      setError(typeof err === 'string' ? err : (err?.message || '切换失败'))
    }
  }

  const openCreate = () => {
    setEditing(null)
    setIsProxyPreset(false)
    setForm(emptyForm)
    setShowForm(true)
  }

  // 一键添加云端 Ele Agent 代理：云端模型列表由公开接口拉取，
  // 选中模型后协议/能力/展示名全部自动带出（云端配置是固定的），用户只需选模型名
  const openProxyPreset = async () => {
    setEditing(null)
    setIsProxyPreset(true)
    setForm({
      ...emptyForm,
      provider: 'eleball',
      protocol: 'openai_compatible',
      base_url: 'https://api.eleball.cn/v1',
      api_key: localStorage.getItem('admin_token') || '',
    })
    setShowForm(true)
    try {
      const data = await eleAgentModelApi.listCloudOptions()
      const list = Array.isArray(data) ? data : (data?.items || [])
      // 按 model_name 去重（同一模型多 provider 时本地配置等价）
      const seen = new Set()
      setCloudOptions(list.filter((o) => {
        if (!o?.model_name || seen.has(o.model_name)) return false
        seen.add(o.model_name)
        return true
      }))
    } catch {
      setCloudOptions([])
    }
  }

  // 选中云端模型：协议、能力、展示名从云端配置带出，无需手填
  const selectProxyModel = (modelName) => {
    const opt = cloudOptions.find((o) => o.model_name === modelName)
    setForm({
      ...form,
      model_name: modelName,
      display_name: opt?.display_name || modelName,
      protocol: opt?.protocol || 'openai_compatible',
      supports_chat: opt ? !!opt.supports_chat : true,
      supports_vision: opt ? !!opt.supports_vision : false,
      supports_image: opt ? !!opt.supports_image : false,
      supports_video: opt ? !!opt.supports_video : false,
      supports_image_input: opt ? !!opt.supports_image_input : false,
      supports_tools: opt ? !!opt.supports_tools : false,
    })
  }

  const openEdit = (m) => {
    setEditing(m)
    setIsProxyPreset(false)
    setForm({
      provider: m.provider || '',
      protocol: m.protocol || 'openai_compatible',
      model_name: m.model_name || '',
      display_name: m.display_name || '',
      base_url: m.base_url || '',
      api_key: '',
      supports_chat: !!m.supports_chat,
      supports_vision: !!m.supports_vision,
      supports_image: !!m.supports_image,
      supports_video: !!m.supports_video,
      supports_image_input: !!m.supports_image_input,
      supports_tools: !!m.supports_tools,
    })
    setShowForm(true)
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      const payload = {
        provider: form.provider.trim(),
        protocol: form.protocol,
        model_name: form.model_name.trim(),
        display_name: form.display_name.trim(),
        base_url: form.base_url.trim(),
        supports_chat: form.supports_chat,
        supports_vision: form.supports_vision,
        supports_image: form.supports_image,
        supports_video: form.supports_video,
        supports_image_input: form.supports_image_input,
        supports_tools: form.supports_tools,
      }
      if (editing) {
        await eleAgentModelApi.update(editing.id, payload)
      } else {
        await eleAgentModelApi.create({ ...payload, api_key: form.api_key.trim() })
      }
      setShowForm(false)
      setEditing(null)
      fetchItems()
    } catch (err) {
      setError(typeof err === 'string' ? err : (err?.message || '提交失败'))
    } finally {
      setSubmitting(false)
    }
  }

  const handleRotateKey = async (m) => {
    const hint = isCloudProxy(m)
      ? '该配置指向云端 Ele Agent：调用时会自动使用你当时的登录态，一般无需换 Key；此处可更新兜底 Key（如用于无登录态的脚本调用）。'
      : '请输入新的 API Key：'
    const key = window.prompt(`更换 ${m.display_name || m.model_name} 的 API Key。\n${hint}`)
    if (!key) return
    setError('')
    try {
      await eleAgentModelApi.rotateKey(m.id, key.trim())
      fetchItems()
    } catch (err) {
      setError(typeof err === 'string' ? err : (err?.message || '换 Key 失败'))
    }
  }

  const handleDelete = async (m) => {
    if (!window.confirm(`确认删除模型配置「${m.display_name || m.model_name}」？`)) return
    setError('')
    try {
      await eleAgentModelApi.remove(m.id)
      fetchItems()
    } catch (err) {
      setError(typeof err === 'string' ? err : (err?.message || '删除失败'))
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-bold text-eleball-text">模型配置</h2>
          <p className="text-sm text-eleball-text-secondary mt-1">
            本地模型配置（BYOK），本地不计费；Ele Agent 云端代理经 gateway 网关，由云端账户计费
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button onClick={openProxyPreset} className="btn-secondary">
            添加 Ele Agent 云端代理
          </button>
          <button onClick={openCreate} className="btn-primary">
            + 新增模型
          </button>
        </div>
      </div>

      {error && <div className="p-3 rounded-xl bg-red-50 text-red-600 text-sm">{error}</div>}

      {/* 配置表单弹窗（BYOK 手填 / Ele Agent 云端代理选模型）*/}
      {showForm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4" onClick={() => !submitting && (setShowForm(false), setEditing(null))}>
          <div className="dialog-panel p-6 w-full max-w-lg max-h-[90vh] overflow-y-auto" onClick={(e) => e.stopPropagation()}>
            <div className="flex items-center justify-between mb-4">
              <h3 className="text-lg font-semibold">
                {editing ? '编辑模型' : isProxyPreset ? '添加 Ele Agent 云端代理' : '新增模型'}
              </h3>
              <button
                onClick={() => { setShowForm(false); setEditing(null) }}
                className="p-1 rounded-full text-eleball-text-tertiary hover:bg-eleball-surface-variant"
              >
                ✕
              </button>
            </div>
            {isProxyPreset && (
              <p className="text-xs text-eleball-text-secondary mb-3">
                云端地址、协议与模型能力均为云端固定配置，选择模型后自动带出；调用时自动使用你当时的最新登录态，无需维护 API Key。
              </p>
            )}
            <form onSubmit={handleSubmit} className="space-y-3">
              {isProxyPreset ? (
                <>
                  <div>
                    <label className="block text-sm font-medium mb-1.5">云端模型 *</label>
                    <select
                      value={form.model_name}
                      onChange={(e) => selectProxyModel(e.target.value)}
                      className="input bg-white" required
                    >
                      <option value="" disabled>
                        {cloudOptions.length > 0 ? '请选择模型' : '模型列表加载中…'}
                      </option>
                      {cloudOptions.map((o) => (
                        <option key={o.model_name} value={o.model_name}>
                          {o.display_name || `${o.provider}/${o.model_name}`}
                        </option>
                      ))}
                    </select>
                  </div>
                  <div>
                    <label className="block text-sm font-medium mb-1.5">展示名称</label>
                    <input
                      value={form.display_name}
                      onChange={(e) => setForm({ ...form, display_name: e.target.value })}
                      className="input bg-white" placeholder="可选，客户端模型列表中显示"
                    />
                  </div>
                </>
              ) : (
                <>
                  <div className="grid grid-cols-2 gap-3">
                    <div>
                      <label className="block text-sm font-medium mb-1.5">平台标识 *</label>
                      <input
                        value={form.provider}
                        onChange={(e) => setForm({ ...form, provider: e.target.value })}
                        className="input bg-white" placeholder="如 kimi / volcengine" required
                      />
                    </div>
                    <div>
                      <label className="block text-sm font-medium mb-1.5">协议</label>
                      <select
                        value={form.protocol}
                        onChange={(e) => setForm({ ...form, protocol: e.target.value })}
                        className="input bg-white"
                      >
                        {PROTOCOLS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
                      </select>
                    </div>
                  </div>
                  <div>
                    <label className="block text-sm font-medium mb-1.5">模型 ID *</label>
                    <input
                      value={form.model_name}
                      onChange={(e) => setForm({ ...form, model_name: e.target.value })}
                      className="input bg-white" placeholder="如 k3 / doubao-seed-2-0-pro-260215" required
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium mb-1.5">展示名称</label>
                    <input
                      value={form.display_name}
                      onChange={(e) => setForm({ ...form, display_name: e.target.value })}
                      className="input bg-white" placeholder="可选，客户端模型列表中显示"
                    />
                  </div>
                  <div>
                    <label className="block text-sm font-medium mb-1.5">Base URL *</label>
                    <input
                      value={form.base_url}
                      onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                      className="input bg-white" placeholder="https://api.example.com/v1" required
                    />
                  </div>
                  {!editing && (
                    <div>
                      <label className="block text-sm font-medium mb-1.5">API Key *</label>
                      <input
                        type="password"
                        value={form.api_key}
                        onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                        className="input bg-white" placeholder="加密存储在本地" required
                      />
                    </div>
                  )}
                  <div>
                    <label className="block text-sm font-medium mb-1.5">能力</label>
                    <div className="flex flex-wrap gap-x-4 gap-y-2">
                      {CAPABILITY_FIELDS.map((f) => (
                        <label key={f.key} className="flex items-center gap-1.5 text-sm text-eleball-text">
                          <input
                            type="checkbox"
                            checked={form[f.key]}
                            onChange={(e) => setForm({ ...form, [f.key]: e.target.checked })}
                            className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                          />
                          {f.label}
                        </label>
                      ))}
                    </div>
                  </div>
                </>
              )}
              <div className="flex justify-end gap-3 pt-2">
                <button type="button" onClick={() => { setShowForm(false); setEditing(null) }} className="btn-ghost">
                  取消
                </button>
                <button type="submit" disabled={submitting} className="btn-primary disabled:opacity-50">
                  {submitting ? '提交中…' : editing ? '保存' : '创建'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* 按 provider 分组的开关式卡片 */}
      {loading && items.length === 0 ? (
        <div className="card text-center text-eleball-text-secondary">加载中...</div>
      ) : items.length === 0 ? (
        <div className="card text-center text-eleball-text-secondary">
          暂无模型配置。点右上角「新增模型」添加 BYOK 模型，或「添加 Ele Agent 云端代理」一键接入云端模型。
        </div>
      ) : (
        <div className="space-y-6">
          {grouped.map(({ provider, list }) => (
            <div key={provider}>
              <div className="flex items-center gap-2 mb-3">
                <h3 className="text-sm font-semibold text-eleball-text">{provider}</h3>
                <span className="text-xs text-eleball-text-tertiary">{list.length} 个模型</span>
                <span className="text-xs text-eleball-text-tertiary">· 启用 {list.filter((i) => i.is_enabled).length}</span>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                {list.map((item) => (
                  <div key={item.id} className="card p-4 flex flex-col gap-3">
                    <div className="flex items-start justify-between gap-3">
                      <div className="min-w-0 flex-1">
                        <div className="font-medium text-eleball-text truncate">{item.display_name || item.model_name}</div>
                        <div className="text-xs text-eleball-text-secondary truncate mt-0.5 font-mono">{item.provider} / {item.model_name}</div>
                      </div>
                      <Toggle checked={!!item.is_enabled} onChange={(v) => handleToggle(item, v)} />
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      <span className="inline-flex px-2 py-0.5 rounded-lg text-xs font-medium bg-blue-50 text-blue-600">
                        {protocolLabel(item.protocol)}
                      </span>
                      {isCloudProxy(item) ? (
                        <CapBadge label="云端代理" cls="bg-eleball-primary/10 text-eleball-primary-dark" />
                      ) : (
                        <CapBadge label="本地 BYOK" cls="bg-gray-100 text-eleball-text-secondary" />
                      )}
                      {item.supports_chat && <CapBadge label="对话" cls="bg-sky-50 text-sky-600" />}
                      {item.supports_vision && <CapBadge label="视觉" cls="bg-purple-50 text-purple-600" />}
                      {item.supports_image && <CapBadge label="图片" cls="bg-indigo-50 text-indigo-600" />}
                      {item.supports_video && <CapBadge label="视频" cls="bg-purple-50 text-purple-600" />}
                      {item.supports_tools && <CapBadge label="工具" cls="bg-emerald-50 text-emerald-600" />}
                    </div>
                    <div className="flex items-center justify-between gap-2 pt-1 border-t border-eleball-outline-variant">
                      <div className="text-[11px] text-eleball-text-tertiary truncate max-w-[140px] font-mono" title={item.base_url}>
                        {item.base_url}
                      </div>
                      <div className="flex gap-1">
                        <button
                          onClick={() => openEdit(item)}
                          className="text-xs px-2 py-1 rounded-lg bg-gray-100 text-eleball-text hover:bg-gray-200 transition-colors"
                        >
                          编辑
                        </button>
                        <button
                          onClick={() => handleRotateKey(item)}
                          className="text-xs px-2 py-1 rounded-lg bg-eleball-primary/10 text-eleball-primary-dark hover:bg-eleball-primary/20 transition-colors"
                        >
                          换 Key
                        </button>
                        <button
                          onClick={() => handleDelete(item)}
                          className="text-xs px-2 py-1 rounded-lg bg-red-50 text-red-600 hover:bg-red-100 transition-colors"
                        >
                          删除
                        </button>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}

      {total > items.length && (
        <div className="text-xs text-eleball-text-tertiary text-center">
          数据较多，仅显示前 {items.length} 条（共 {total} 条）。
        </div>
      )}
    </div>
  )
}

const emptyForm = {
  provider: '',
  protocol: 'openai_compatible',
  model_name: '',
  display_name: '',
  base_url: '',
  api_key: '',
  supports_chat: true,
  supports_vision: false,
  supports_image: false,
  supports_video: false,
  supports_image_input: false,
  supports_tools: false,
}
