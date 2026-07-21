import { useEffect, useState } from 'react'
import { settingsApi, eleAgentModelApi } from '../api/client'

// 后端 settings 字段为 snake_case，前端页面使用驼峰
function fromBackend(data) {
  if (!data) return null
  return {
    siteName: data.site_name ?? 'Eleball',
    registerOpen: data.register_open ?? true,
    defaultModel: data.default_model ?? 'qwen/Qwen/Qwen3-8B',
    maxTokensPerRequest: data.max_tokens_per_request ?? 4096,
    freeQuota: data.free_quota ?? 1000,
    maintenanceMode: data.maintenance_mode ?? false,
    xianyuProductUrl: data.xianyu_product_url ?? '',
    taobaoProductUrl: data.taobao_product_url ?? '',
    promptFusionModel: data.prompt_fusion_model ?? ''
  }
}

function toBackend(settings) {
  return {
    site_name: settings.siteName,
    register_open: settings.registerOpen,
    default_model: settings.defaultModel,
    max_tokens_per_request: settings.maxTokensPerRequest,
    free_quota: settings.freeQuota,
    maintenance_mode: settings.maintenanceMode,
    xianyu_product_url: settings.xianyuProductUrl,
    taobao_product_url: settings.taobaoProductUrl,
    prompt_fusion_model: settings.promptFusionModel
  }
}

export default function Settings() {
  const [settings, setSettings] = useState({
    siteName: 'Eleball',
    registerOpen: true,
    defaultModel: 'qwen/Qwen/Qwen3-8B',
    maxTokensPerRequest: 4096,
    freeQuota: 1000,
    maintenanceMode: false,
    xianyuProductUrl: '',
    taobaoProductUrl: '',
    promptFusionModel: ''
  })
  const [fusionModels, setFusionModels] = useState([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    const fetchData = async () => {
      setLoading(true)
      try {
        const [settingsRes, modelsRes] = await Promise.all([
          settingsApi.get(),
          eleAgentModelApi.list(1, 100)
        ])
        const mapped = fromBackend(settingsRes)
        if (mapped) setSettings(mapped)
        // 仅保留支持文字对话的模型作为 prompt 融合候选（需能执行对话补全）
        const items = Array.isArray(modelsRes?.items) ? modelsRes.items : []
        const candidates = items.filter((m) => m.is_enabled && m.supports_chat === true)
        setFusionModels(candidates)
      } catch (err) {
        console.error('加载设置失败', err)
        alert('加载设置失败：' + (err?.message || err))
      } finally {
        setLoading(false)
      }
    }
    fetchData()
  }, [])

  const handleChange = (key, value) => {
    setSettings(prev => ({ ...prev, [key]: value }))
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      await settingsApi.update(toBackend(settings))
      alert('设置已保存')
    } catch (err) {
      alert('保存失败：' + (err?.message || err))
    } finally {
      setSaving(false)
    }
  }

  const handleReset = async () => {
    if (!window.confirm('确定重置为默认设置？')) return
    try {
      const res = await settingsApi.get()
      const mapped = fromBackend(res)
      if (mapped) setSettings(mapped)
    } catch (err) {
      alert('重置失败：' + (err?.message || err))
    }
  }

  return (
    <div className="space-y-6 max-w-3xl">
      <div>
        <h1 className="text-2xl font-bold">系统设置</h1>
        <p className="text-eleball-text-secondary mt-1">配置平台核心参数与运营策略</p>
      </div>

      {loading && (
        <div className="text-sm text-eleball-text-secondary">加载中...</div>
      )}

      <div className="card space-y-6">
        <h3 className="text-base font-semibold">基础配置</h3>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
          <div>
            <label className="block text-sm font-medium mb-1.5">站点名称</label>
            <input
              type="text"
              value={settings.siteName}
              onChange={(e) => handleChange('siteName', e.target.value)}
              className="input"
            />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">默认模型</label>
            <select
              value={settings.defaultModel}
              onChange={(e) => handleChange('defaultModel', e.target.value)}
              className="input"
            >
              <option value="qwen/Qwen/Qwen3-8B">通义千问 Qwen3-8B</option>
              <option value="deepseek-chat">DeepSeek Chat</option>
              <option value="gpt-3.5-turbo">GPT-3.5 Turbo</option>
              <option value="gpt-4">GPT-4</option>
            </select>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">视觉 Prompt 融合模型</label>
            <select
              value={settings.promptFusionModel}
              onChange={(e) => handleChange('promptFusionModel', e.target.value)}
              className="input"
            >
              <option value="">未配置（单次任务型视觉模型将不支持连续创作）</option>
              {fusionModels.map((m) => {
                const value = `${m.provider}/${m.model_name}`
                return (
                  <option key={value} value={value}>
                    {m.display_name || value}
                  </option>
                )
              })}
            </select>
            <p className="text-xs text-eleball-text-secondary mt-1.5">
              用于 Agnes/Seedance 等单次任务型视觉协议的连续创作 prompt 融合。需选择已启用的对话模型。
            </p>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">单次最大 Token</label>
            <input
              type="number"
              value={settings.maxTokensPerRequest}
              onChange={(e) => handleChange('maxTokensPerRequest', parseInt(e.target.value) || 0)}
              className="input"
            />
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">新用户免费额度（Token）</label>
            <input
              type="number"
              value={settings.freeQuota}
              onChange={(e) => handleChange('freeQuota', parseInt(e.target.value) || 0)}
              className="input"
            />
          </div>
        </div>
      </div>

      <div className="card space-y-6">
        <h3 className="text-base font-semibold">运营链接</h3>

        <div className="grid grid-cols-1 gap-6">
          <div>
            <label className="block text-sm font-medium mb-1.5">闲鱼商品链接</label>
            <input
              type="url"
              value={settings.xianyuProductUrl}
              onChange={(e) => handleChange('xianyuProductUrl', e.target.value)}
              placeholder="https://www.goofish.com/item?id=xxx"
              className="input"
            />
            <p className="text-xs text-eleball-text-secondary mt-1.5">
              配置后，用户端充值页面会在兑换码充值下方展示闲鱼入口图标。
            </p>
          </div>
          <div>
            <label className="block text-sm font-medium mb-1.5">淘宝商品链接</label>
            <input
              type="url"
              value={settings.taobaoProductUrl}
              onChange={(e) => handleChange('taobaoProductUrl', e.target.value)}
              placeholder="https://item.taobao.com/item.htm?id=xxx"
              className="input"
            />
            <p className="text-xs text-eleball-text-secondary mt-1.5">
              配置后，用户端充值页面会在兑换码充值下方展示淘宝入口图标。
            </p>
          </div>
        </div>
      </div>

      <div className="card space-y-6">
        <h3 className="text-base font-semibold">功能开关</h3>

        <div className="space-y-4">
          <label className="flex items-center justify-between py-3 border-b border-eleball-outline last:border-0 cursor-pointer">
            <div>
              <p className="font-medium">开放注册</p>
              <p className="text-sm text-eleball-text-secondary">关闭后仅支持管理员手动创建账号</p>
            </div>
            <input
              type="checkbox"
              checked={settings.registerOpen}
              onChange={(e) => handleChange('registerOpen', e.target.checked)}
              className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
            />
          </label>

          <label className="flex items-center justify-between py-3 cursor-pointer">
            <div>
              <p className="font-medium">维护模式</p>
              <p className="text-sm text-eleball-text-secondary">开启后仅管理员可登录，普通用户看到维护页面</p>
            </div>
            <input
              type="checkbox"
              checked={settings.maintenanceMode}
              onChange={(e) => handleChange('maintenanceMode', e.target.checked)}
              className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
            />
          </label>
        </div>
      </div>

      <div className="flex gap-3">
        <button onClick={handleSave} disabled={saving} className="btn-primary disabled:opacity-60">
          {saving ? '保存中...' : '保存设置'}
        </button>
        <button onClick={handleReset} className="btn-secondary">
          重置
        </button>
      </div>
    </div>
  )
}
