/**
 * 模型配置管理
 * 对齐 App 端 ModelProfile / ProviderType 设计
 */

import { getJSON, setJSON, getItem, setItem, removeItem } from './storage'

export const PROVIDERS = {
  ELE_AGENT: { label: 'Ele Agent', byok: false, defaultBaseUrl: '' },
  OPENAI: { label: 'OpenAI', byok: true, defaultBaseUrl: 'https://api.openai.com/v1' },
  DEEPSEEK: { label: 'DeepSeek', byok: true, defaultBaseUrl: 'https://api.deepseek.com/v1' },
  QWEN: { label: '通义千问', byok: true, defaultBaseUrl: 'https://api.siliconflow.cn/v1' },
  MOONSHOT: { label: 'Moonshot', byok: true, defaultBaseUrl: 'https://api.moonshot.cn/v1' },
  CUSTOM: { label: '自定义', byok: true, defaultBaseUrl: '' }
}

/**
 * Ele Agent 子平台（模型供应商）显示名映射
 * 未命中时回退到 provider key，保持与后端契约无关
 */
export const PROVIDER_LABELS = {
  qwen: '通义千问',
  openai: 'OpenAI',
  deepseek: 'DeepSeek',
  anthropic: 'Anthropic',
  moonshot: 'Moonshot',
  gemini: 'Gemini',
  bailian: '百炼',
  zhipu: '智谱',
  hunyuan: '腾讯混元'
}

export function getProviderLabel(provider) {
  return PROVIDER_LABELS[provider] || provider || '未知平台'
}

/**
 * 将 Ele Agent 模型列表按 provider（模型供应商）分组
 * @param {Array} models 后端 /eleagent/models 返回的扁平模型列表
 * @returns {Array<{provider: string, providerLabel: string, models: Array}>}
 */
export function groupEleAgentModelsByProvider(models) {
  if (!Array.isArray(models) || models.length === 0) return []

  const groups = {}
  models.forEach((m) => {
    const p = m.provider || '其他'
    if (!groups[p]) groups[p] = []
    groups[p].push(m)
  })

  return Object.keys(groups)
    .sort()
    .map((p) => ({
      provider: p,
      providerLabel: getProviderLabel(p),
      models: groups[p]
    }))
}

// claw 单文件二进制无反代，API 直连 /v1
export const API_BASE = import.meta.env.VITE_API_BASE || '/v1'

export function createDefaultEleAgentProfile(defaultModelProfile) {
  return {
    id: 'eleagent_default',
    name: 'Ele Agent',
    provider: 'ELE_AGENT',
    modelName: defaultModelProfile?.model_name || 'qwen/Qwen/Qwen3.5-4B',
    baseUrl: '',
    apiKey: '',
    systemPrompt: defaultModelProfile?.system_prompt || '',
    temperature: 0.7,
    isDefault: true
  }
}

const PROFILES_KEY = 'model_profiles'
const CURRENT_PROFILE_ID_KEY = 'current_profile_id'

function profilesKey(userId) {
  return userId ? `${PROFILES_KEY}_${userId}` : PROFILES_KEY
}

function currentProfileIdKey(userId) {
  return userId ? `${CURRENT_PROFILE_ID_KEY}_${userId}` : CURRENT_PROFILE_ID_KEY
}

/**
 * 加载模型 Profile，按 userId 隔离。
 * 如果当前用户没有专属配置，回退到旧的无作用域 key 做数据迁移。
 */
export function loadProfiles(defaultModelProfile, userId = null) {
  const key = profilesKey(userId)
  let profiles = getJSON(key, null)
  if (!Array.isArray(profiles) || profiles.length === 0) {
    // 迁移旧数据：无作用域的 key 里可能存有当前设备上唯一的 Profile 列表
    if (userId) {
      profiles = getJSON(PROFILES_KEY, null)
    }
    if (!Array.isArray(profiles) || profiles.length === 0) {
      const defaultProfile = createDefaultEleAgentProfile(defaultModelProfile)
      saveProfiles([defaultProfile], userId)
      return [defaultProfile]
    }
  }
  // 过滤掉 localStorage 中可能损坏的 null/undefined/非对象项，避免渲染时报错
  const validProfiles = profiles.filter((p) => p && typeof p === 'object' && p.id)
  if (validProfiles.length === 0) {
    const defaultProfile = createDefaultEleAgentProfile(defaultModelProfile)
    saveProfiles([defaultProfile], userId)
    return [defaultProfile]
  }
  return validProfiles
}

export function saveProfiles(profiles, userId = null) {
  setJSON(profilesKey(userId), profiles)
  // 一旦写入用户隔离数据，就删除旧的无作用域 key，避免其他账号继承
  if (userId) {
    removeItem(PROFILES_KEY)
  }
}

export function loadCurrentProfileId(userId = null) {
  let id = getItem(currentProfileIdKey(userId))
  if (!id && userId) {
    id = getItem(CURRENT_PROFILE_ID_KEY)
  }
  return id
}

export function saveCurrentProfileId(id, userId = null) {
  setItem(currentProfileIdKey(userId), id)
  if (userId) {
    removeItem(CURRENT_PROFILE_ID_KEY)
  }
}

export function resolveBaseUrl(rawBaseUrl) {
  const lowered = (rawBaseUrl || '').toLowerCase()
  if (lowered.includes('://localhost') || lowered.includes('://127.0.0.1') || !rawBaseUrl) {
    return API_BASE
  }
  return rawBaseUrl.replace(/\/$/, '')
}

export function parseEleAgentModelName(modelName) {
  const idx = modelName.indexOf('/')
  if (idx <= 0) return { subProvider: '', subModel: '' }
  return {
    subProvider: modelName.slice(0, idx),
    subModel: modelName.slice(idx + 1)
  }
}

export function loadCachedCredentials(profileId, userId = null) {
  const key = userId ? `credentials_${userId}_${profileId}` : `credentials_${profileId}`
  return getJSON(key, null)
}

export function saveCachedCredentials(profileId, credentials, userId = null) {
  const key = userId ? `credentials_${userId}_${profileId}` : `credentials_${profileId}`
  setJSON(key, credentials)
}

export function clearCachedCredentials(profileId, userId = null) {
  const key = userId ? `credentials_${userId}_${profileId}` : `credentials_${profileId}`
  localStorage.removeItem(`eleball_${key}`)
}
