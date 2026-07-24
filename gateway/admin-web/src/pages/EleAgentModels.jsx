import { useEffect, useState } from 'react'
import { eleAgentModelApi } from '../api/client'

/**
 * claw 模型配置（本地 CRUD）。
 * 支持 BYOK 添加自有模型（任意 OpenAI 兼容/厂商协议端点），
 * 以及一键添加云端 Ele Agent 代理（Base URL 指向 api.eleball.cn，API Key 用当前登录态）。
 * 本地不计费，不展示价格字段；Ele Agent 代理调用由云端账户计费。
 */

const PROTOCOLS = [
  { value: 'openai_compatible', label: 'OpenAI 兼容' },
  { value: 'anthropic_messages', label: 'Anthropic Messages' },
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

export default function EleAgentModels() {
  const [items, setItems] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const pageSize = 20

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
      const data = await eleAgentModelApi.list(page, pageSize)
      const list = data?.items ?? []
      setItems(Array.isArray(list) ? list : [])
      setTotal(data?.total ?? 0)
      const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / pageSize))
      if (page > totalPages) setPage(totalPages)
    } catch (err) {
      setError(typeof err === 'string' ? err : (err?.message || '加载失败'))
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchItems()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page])

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
    const hint = m.base_url?.includes('api.eleball.cn')
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

  const handleToggle = async (m) => {
    setError('')
    try {
      await eleAgentModelApi.update(m.id, { is_enabled: !m.is_enabled })
      fetchItems()
    } catch (err) {
      setError(typeof err === 'string' ? err : (err?.message || '操作失败'))
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

  const totalPages = Math.max(1, Math.ceil(total / pageSize))

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">模型配置</h1>
          <p className="text-sm text-eleball-text-secondary mt-1">
            本地模型配置（BYOK），本地不计费；Ele Agent 云端代理由云端账户计费
          </p>
        </div>
        <div className="flex gap-2">
          <button onClick={openProxyPreset} className="px-4 py-2 rounded-xl border border-eleball-primary text-eleball-primary text-sm font-medium hover:bg-eleball-primary-light transition-colors">
            添加 Ele Agent 云端代理
          </button>
          <button onClick={openCreate} className="btn-primary text-sm px-4 py-2">
            新增模型
          </button>
        </div>
      </div>

      {error && <div className="p-3 rounded-xl bg-red-50 text-red-600 text-sm">{error}</div>}

      <div className="card p-6">
        <h2 className="text-lg font-semibold mb-4">模型列表</h2>
        {loading ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">加载中…</div>
        ) : items.length === 0 ? (
          <div className="text-center py-8 text-sm text-eleball-text-secondary">
            暂无模型配置。点右上角「新增模型」添加 BYOK 模型，或「添加 Ele Agent 云端代理」一键接入云端模型。
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm text-left">
              <thead className="text-xs text-eleball-text-secondary border-b border-eleball-outline">
                <tr>
                  <th className="px-3 py-2 font-medium">模型</th>
                  <th className="px-3 py-2 font-medium">协议</th>
                  <th className="px-3 py-2 font-medium">Base URL</th>
                  <th className="px-3 py-2 font-medium">能力</th>
                  <th className="px-3 py-2 font-medium">状态</th>
                  <th className="px-3 py-2 font-medium">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-eleball-outline-variant">
                {items.map((m) => (
                  <tr key={m.id}>
                    <td className="px-3 py-2.5">
                      <div className="font-medium">{m.display_name || `${m.provider}/${m.model_name}`}</div>
                      <div className="text-xs text-eleball-text-secondary font-mono">{m.provider}/{m.model_name}</div>
                    </td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary">
                      {PROTOCOLS.find((p) => p.value === m.protocol)?.label || m.protocol || '-'}
                    </td>
                    <td className="px-3 py-2.5 text-xs text-eleball-text-secondary font-mono max-w-[220px] truncate" title={m.base_url}>
                      {m.base_url}
                    </td>
                    <td className="px-3 py-2.5">
                      <div className="flex flex-wrap gap-1">
                        {CAPABILITY_FIELDS.filter((f) => m[f.key]).map((f) => (
                          <span key={f.key} className="text-[10px] px-1.5 py-0.5 rounded bg-gray-100 text-eleball-text-secondary">
                            {f.label}
                          </span>
                        ))}
                      </div>
                    </td>
                    <td className="px-3 py-2.5">
                      <button
                        onClick={() => handleToggle(m)}
                        className={`text-xs px-2 py-0.5 rounded ${m.is_enabled ? 'bg-emerald-50 text-emerald-600' : 'bg-gray-100 text-gray-500'}`}
                        title="点击切换启用状态"
                      >
                        {m.is_enabled ? '启用' : '禁用'}
                      </button>
                    </td>
                    <td className="px-3 py-2.5">
                      <div className="flex gap-2 text-xs">
                        <button onClick={() => handleRotateKey(m)} className="text-eleball-primary hover:underline">换 Key</button>
                        <button onClick={() => openEdit(m)} className="text-eleball-primary hover:underline">编辑</button>
                        <button onClick={() => handleDelete(m)} className="text-red-500 hover:underline">删除</button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* 分页 */}
        {!loading && total > 0 && (
          <div className="flex items-center justify-between mt-4">
            <span className="text-sm text-eleball-text-secondary">共 {total} 条记录</span>
            <div className="flex gap-2">
              <button
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={page === 1}
                className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-40"
              >
                上一页
              </button>
              <span className="px-3 py-1.5 rounded-lg bg-eleball-primary text-white text-sm font-medium">
                {page} / {totalPages}
              </span>
              <button
                onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                disabled={page >= totalPages}
                className="px-3 py-1.5 rounded-lg border border-eleball-outline text-sm disabled:opacity-40"
              >
                下一页
              </button>
            </div>
          </div>
        )}
      </div>

      {/* 新建 / 编辑弹窗 */}
      {showForm && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4" onClick={() => setShowForm(false)}>
          <div className="bg-white rounded-2xl shadow-xl w-full max-w-lg max-h-[90vh] overflow-y-auto p-6" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-lg font-semibold mb-1">
              {editing ? '编辑模型' : isProxyPreset ? '添加 Ele Agent 云端代理' : '新增模型'}
            </h3>
            {isProxyPreset && (
              <p className="text-xs text-eleball-text-secondary mb-3">
                云端地址、协议与模型能力均为云端固定配置，选择模型后自动带出；调用时自动使用你当时的最新登录态，无需维护 API Key。
              </p>
            )}
            <form onSubmit={handleSubmit} className="space-y-3 mt-3">
              {isProxyPreset ? (
                <>
                  <div>
                    <label className="block text-xs font-medium mb-1">云端模型 *</label>
                    <select
                      value={form.model_name}
                      onChange={(e) => selectProxyModel(e.target.value)}
                      className="input" required
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
                    <label className="block text-xs font-medium mb-1">展示名称</label>
                    <input
                      value={form.display_name}
                      onChange={(e) => setForm({ ...form, display_name: e.target.value })}
                      className="input" placeholder="可选，客户端模型列表中显示"
                    />
                  </div>
                </>
              ) : (
                <>
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="block text-xs font-medium mb-1">平台标识 *</label>
                  <input
                    value={form.provider}
                    onChange={(e) => setForm({ ...form, provider: e.target.value })}
                    className="input" placeholder="如 kimi / volcengine" required
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium mb-1">协议</label>
                  <select
                    value={form.protocol}
                    onChange={(e) => setForm({ ...form, protocol: e.target.value })}
                    className="input"
                  >
                    {PROTOCOLS.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}
                  </select>
                </div>
              </div>
              <div>
                <label className="block text-xs font-medium mb-1">模型 ID *</label>
                <input
                  value={form.model_name}
                  onChange={(e) => setForm({ ...form, model_name: e.target.value })}
                  className="input" placeholder="如 k3 / doubao-seed-2-0-pro-260215" required
                />
              </div>
              <div>
                <label className="block text-xs font-medium mb-1">展示名称</label>
                <input
                  value={form.display_name}
                  onChange={(e) => setForm({ ...form, display_name: e.target.value })}
                  className="input" placeholder="可选，客户端模型列表中显示"
                />
              </div>
              <div>
                <label className="block text-xs font-medium mb-1">Base URL *</label>
                <input
                  value={form.base_url}
                  onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                  className="input" placeholder="https://api.example.com/v1" required
                />
              </div>
              {!editing && (
                <div>
                  <label className="block text-xs font-medium mb-1">API Key *</label>
                  <input
                    type="password"
                    value={form.api_key}
                    onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                    className="input" placeholder="加密存储在本地" required
                  />
                </div>
              )}
              <div>
                <label className="block text-xs font-medium mb-1.5">能力</label>
                <div className="flex flex-wrap gap-x-4 gap-y-2">
                  {CAPABILITY_FIELDS.map((f) => (
                    <label key={f.key} className="flex items-center gap-1.5 text-xs text-eleball-text">
                      <input
                        type="checkbox"
                        checked={form[f.key]}
                        onChange={(e) => setForm({ ...form, [f.key]: e.target.checked })}
                      />
                      {f.label}
                    </label>
                  ))}
                </div>
              </div>
                </>
              )}
              <div className="flex justify-end gap-2 pt-2">
                <button type="button" onClick={() => setShowForm(false)} className="px-4 py-2 rounded-xl border border-eleball-outline text-sm">
                  取消
                </button>
                <button type="submit" disabled={submitting} className="btn-primary text-sm px-4 py-2 disabled:opacity-50">
                  {submitting ? '提交中…' : editing ? '保存' : '创建'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  )
}
