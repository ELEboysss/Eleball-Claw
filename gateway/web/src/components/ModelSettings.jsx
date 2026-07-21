import { useState, useEffect, useMemo } from 'react'
import { X, Plus, Star, Trash2, Edit2, Bot, Key, Server, ChevronDown } from 'lucide-react'
import { PROVIDERS, resolveBaseUrl, parseEleAgentModelName, groupEleAgentModelsByProvider } from '../utils/model'

const emptyProfile = {
  id: '',
  name: '',
  provider: 'ELE_AGENT',
  modelName: '',
  baseUrl: '',
  apiKey: '',
  systemPrompt: '',
  temperature: 0.7,
  isDefault: false
}

export default function ModelSettings({
  open,
  onClose,
  profiles,
  currentProfileId,
  eleagentModels,
  onProfilesChange,
  onCurrentChange
}) {
  const fullModelName = (m) => `${m.provider}/${m.model_name}`

  const [mode, setMode] = useState('list')
  const [editing, setEditing] = useState(null)
  const [form, setForm] = useState(emptyProfile)

  useEffect(() => {
    if (open) {
      setMode('list')
      setEditing(null)
      setForm(emptyProfile)
    }
  }, [open])

  // 过滤掉可能损坏的 profile，避免 localStorage 脏数据导致渲染报错
  const validProfiles = useMemo(() => (Array.isArray(profiles) ? profiles.filter(Boolean) : []), [profiles])

  // Ele Agent 模型按供应商分组
  const groupedModels = useMemo(() => groupEleAgentModelsByProvider(eleagentModels), [eleagentModels])

  if (!open) return null

  const handleAdd = () => {
    setEditing(null)
    const first = eleagentModels[0]
    setForm({
      ...emptyProfile,
      id: `profile_${Date.now()}`,
      provider: 'ELE_AGENT',
      modelName: first ? fullModelName(first) : '',
      name: first?.display_name || 'Ele Agent'
    })
    setMode('form')
  }

  const handleEdit = (profile) => {
    setEditing(profile)
    setForm({ ...profile })
    setMode('form')
  }

  const handleSave = () => {
    if (!form.name.trim() || !form.modelName.trim()) return

    let next = [...profiles]
    if (form.isDefault) {
      next = next.map((p) => ({ ...p, isDefault: false }))
    }

    if (editing) {
      next = next.map((p) => (p.id === editing.id ? form : p))
    } else {
      // 第一个配置自动设为默认
      if (next.length === 0) form.isDefault = true
      next.push(form)
    }

    // 保证有一个默认
    if (!next.some((p) => p.isDefault)) {
      next[0].isDefault = true
    }

    onProfilesChange(next)
    if (form.isDefault || !currentProfileId) {
      onCurrentChange(form.id)
    }
    setMode('list')
  }

  const handleDelete = (id) => {
    if (profiles.length <= 1) return
    let next = profiles.filter((p) => p.id !== id)
    if (!next.some((p) => p.isDefault)) {
      next[0].isDefault = true
    }
    onProfilesChange(next)
    if (currentProfileId === id) {
      onCurrentChange(next.find((p) => p.isDefault)?.id || next[0].id)
    }
  }

  const handleSetDefault = (id) => {
    // 仅将该模型标记为打开新对话时的默认初始模型，不切换当前对话正在使用的模型
    const next = profiles.map((p) => ({ ...p, isDefault: p.id === id }))
    onProfilesChange(next)
  }

  const providerInfo = PROVIDERS[form.provider] || { label: form.provider || '未知', defaultBaseUrl: '' }
  const isEleAgent = form.provider === 'ELE_AGENT'
  const currentProvider = isEleAgent ? parseEleAgentModelName(form.modelName).subProvider : ''
  const currentGroup = groupedModels.find((g) => g.provider === currentProvider) || groupedModels[0]

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center px-4 bg-black/40 backdrop-blur-sm">
      <div className="relative w-full max-w-md bg-eleball-surface rounded-3xl p-6 shadow-xl max-h-[85vh] overflow-y-auto">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 p-1 rounded-full text-eleball-text-tertiary hover:bg-eleball-surface-variant"
        >
          <X className="w-5 h-5" />
        </button>

        <h2 className="text-xl font-bold text-eleball-text mb-1">模型配置</h2>
        <p className="text-sm text-eleball-text-secondary mb-5">与 App 端一致的模型 Profile 管理</p>

        {mode === 'list' ? (
          <div className="space-y-3">
            {validProfiles.map((profile) => (
              <div
                key={profile.id}
                onClick={() => onCurrentChange(profile.id)}
                className={`flex items-center gap-3 p-3 rounded-xl border transition-colors cursor-pointer ${
                  profile.id === currentProfileId
                    ? 'border-eleball-primary bg-eleball-primary-light/30'
                    : 'border-eleball-outline-variant hover:border-eleball-primary/50'
                }`}
              >
                <div className={`w-10 h-10 rounded-full flex items-center justify-center shrink-0 ${
                  profile.provider === 'ELE_AGENT' ? 'bg-eleball-primary-light' : 'bg-eleball-surface-variant'
                }`}>
                  {profile.provider === 'ELE_AGENT' ? (
                    <Bot className="w-5 h-5 text-eleball-primary" />
                  ) : (
                    <Key className="w-5 h-5 text-eleball-secondary" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-sm font-medium text-eleball-text truncate">{profile.name}</span>
                    {profile.isDefault && (
                      <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-eleball-primary text-white">默认</span>
                    )}
                  </div>
                  <div className="text-xs text-eleball-text-tertiary truncate">
                    {PROVIDERS[profile.provider]?.label} · {profile.modelName}
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  {!profile.isDefault && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        handleSetDefault(profile.id)
                      }}
                      className="p-1.5 rounded-lg text-eleball-text-tertiary hover:bg-eleball-surface-variant"
                      title="设为默认初始模型"
                    >
                      <Star className="w-4 h-4" />
                    </button>
                  )}
                  <button
                    onClick={(e) => {
                      e.stopPropagation()
                      handleEdit(profile)
                    }}
                    className="p-1.5 rounded-lg text-eleball-text-tertiary hover:bg-eleball-surface-variant"
                    title="编辑"
                  >
                    <Edit2 className="w-4 h-4" />
                  </button>
                  {profiles.length > 1 && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation()
                        handleDelete(profile.id)
                      }}
                      className="p-1.5 rounded-lg text-eleball-error hover:bg-red-50"
                      title="删除"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  )}
                </div>
              </div>
            ))}

            <button
              onClick={handleAdd}
              className="w-full flex items-center justify-center gap-2 py-2.5 text-sm font-medium text-eleball-primary bg-eleball-primary-light/50 rounded-xl hover:bg-eleball-primary-light"
            >
              <Plus className="w-4 h-4" />
              添加模型配置
            </button>
          </div>
        ) : (
          <div className="space-y-4">
            <div>
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">配置名称</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
                placeholder="例如：Ele Agent / 我的 DeepSeek"
                className="input w-full"
              />
            </div>

            <div>
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">服务商</label>
              <div className="relative">
                <select
                  value={form.provider}
                  onChange={(e) => {
                    const provider = e.target.value
                    const info = PROVIDERS[provider] || { defaultBaseUrl: '' }
                    setForm({
                      ...form,
                      provider,
                      baseUrl: info.defaultBaseUrl,
                      apiKey: '',
                      modelName: provider === 'ELE_AGENT' ? (eleagentModels[0] ? fullModelName(eleagentModels[0]) : '') : ''
                    })
                  }}
                  className="input w-full appearance-none"
                >
                  {Object.entries(PROVIDERS).map(([key, info]) => (
                    <option key={key} value={key}>{info?.label || key}</option>
                  ))}
                </select>
                <ChevronDown className="w-4 h-4 text-eleball-text-tertiary absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none" />
              </div>
            </div>

            {isEleAgent ? (
              <>
                <div>
                  <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">模型供应商</label>
                  <div className="relative">
                    <select
                      value={currentProvider}
                      onChange={(e) => {
                        const provider = e.target.value
                        const group = groupedModels.find((g) => g.provider === provider) || groupedModels[0]
                        const firstModel = group?.models[0]
                        const modelName = firstModel ? fullModelName(firstModel) : ''
                        setForm({
                          ...form,
                          modelName,
                          name: firstModel?.display_name || form.name
                        })
                      }}
                      className="input w-full appearance-none"
                      disabled={groupedModels.length === 0}
                    >
                      {groupedModels.map((g) => (
                        <option key={g.provider} value={g.provider}>
                          {g.providerLabel}
                        </option>
                      ))}
                    </select>
                    <ChevronDown className="w-4 h-4 text-eleball-text-tertiary absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                </div>

                <div>
                  <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">模型</label>
                  <div className="relative">
                    <select
                      value={form.modelName}
                      onChange={(e) => {
                        const modelName = e.target.value
                        const found = eleagentModels.find((m) => fullModelName(m) === modelName)
                        setForm({ ...form, modelName, name: found?.display_name || form.name })
                      }}
                      className="input w-full appearance-none"
                      disabled={!currentGroup || currentGroup.models.length === 0}
                    >
                      {currentGroup?.models.map((m) => (
                        <option key={fullModelName(m)} value={fullModelName(m)}>
                          {m.display_name || m.model_name}
                        </option>
                      ))}
                    </select>
                    <ChevronDown className="w-4 h-4 text-eleball-text-tertiary absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none" />
                  </div>
                </div>
              </>
            ) : (
              <>
                <div>
                  <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">Base URL</label>
                  <input
                    type="text"
                    value={form.baseUrl}
                    onChange={(e) => setForm({ ...form, baseUrl: e.target.value })}
                    placeholder={providerInfo?.defaultBaseUrl || 'https://api.example.com/v1'}
                    className="input w-full"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">模型名</label>
                  <input
                    type="text"
                    value={form.modelName}
                    onChange={(e) => setForm({ ...form, modelName: e.target.value })}
                    placeholder="gpt-4o / deepseek-chat"
                    className="input w-full"
                  />
                </div>
                <div>
                  <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">API Key</label>
                  <input
                    type="password"
                    value={form.apiKey}
                    onChange={(e) => setForm({ ...form, apiKey: e.target.value })}
                    placeholder="sk-..."
                    className="input w-full"
                  />
                  <p className="text-xs text-eleball-text-tertiary mt-1.5">
                    BYOK 模式下 Key 仅保存在浏览器本地，直接调用模型商端点。
                  </p>
                </div>
              </>
            )}

            <div>
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1.5">System Prompt（可选）</label>
              <textarea
                value={form.systemPrompt}
                onChange={(e) => setForm({ ...form, systemPrompt: e.target.value })}
                placeholder="你是..."
                rows={2}
                className="input w-full resize-none"
              />
            </div>

            <label className="flex items-center gap-2 text-sm text-eleball-text-secondary cursor-pointer">
              <input
                type="checkbox"
                checked={form.isDefault}
                onChange={(e) => setForm({ ...form, isDefault: e.target.checked })}
                className="accent-eleball-primary"
              />
              设为默认配置
            </label>

            <div className="flex gap-3 pt-2">
              <button
                onClick={() => setMode('list')}
                className="flex-1 py-2.5 text-sm font-medium text-eleball-text-secondary bg-eleball-surface-variant rounded-xl hover:bg-eleball-outline-variant"
              >
                取消
              </button>
              <button
                onClick={handleSave}
                disabled={!form.name.trim() || !form.modelName.trim()}
                className="flex-1 py-2.5 text-sm font-medium text-white bg-eleball-primary rounded-xl hover:bg-eleball-primary-dark disabled:opacity-50"
              >
                保存
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
