/**
 * 安全读写 localStorage，避免非法内容导致 JSON.parse 崩溃
 */

const PREFIX = 'eleball_'

export function safeJSONParse(raw, fallback = null) {
  if (!raw || raw === 'undefined' || raw === 'null') return fallback
  try {
    return JSON.parse(raw)
  } catch {
    return fallback
  }
}

export function getItem(key) {
  return localStorage.getItem(`${PREFIX}${key}`)
}

export function setItem(key, value) {
  localStorage.setItem(`${PREFIX}${key}`, value)
}

export function removeItem(key) {
  localStorage.removeItem(`${PREFIX}${key}`)
}

export function getJSON(key, fallback = null) {
  return safeJSONParse(getItem(key), fallback)
}

export function setJSON(key, value) {
  setItem(key, JSON.stringify(value))
}

/**
 * 获取或生成 Web 端 device_id，用于登录/注册
 */
export function getDeviceId() {
  let deviceId = getItem('device_id')
  if (!deviceId) {
    deviceId = crypto.randomUUID ? crypto.randomUUID() : `web-${Date.now()}-${Math.random().toString(36).slice(2)}`
    setItem('device_id', deviceId)
  }
  return deviceId
}
