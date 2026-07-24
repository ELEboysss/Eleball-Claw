// 视觉生成任务本地工具函数

export const MEDIA_TYPES = {
  IMAGE: 'image',
  VIDEO: 'video'
}

export const STATUS_LABELS = {
  pending: '排队中',
  running: '生成中',
  succeeded: '已完成',
  failed: '失败',
  cancelled: '已取消'
}

export const STATUS_COLORS = {
  pending: 'text-yellow-400',
  running: 'text-blue-400',
  succeeded: 'text-green-400',
  failed: 'text-red-400',
  cancelled: 'text-gray-400'
}

export function isTerminal(status) {
  return status === 'succeeded' || status === 'failed' || status === 'cancelled'
}

export function formatCost(cost, currency = 'danwan') {
  if (cost === undefined || cost === null) return '-'
  const unit = currency === 'danwan' ? '弹丸' : currency
  return `${cost} ${unit}`
}

export function formatDuration(seconds) {
  if (!seconds && seconds !== 0) return '-'
  if (seconds < 60) return `${seconds}秒`
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return s ? `${m}分${s}秒` : `${m}分`
}

// claw 单文件二进制无反代，API 直连 /v1
const API_BASE = import.meta.env.VITE_API_BASE || '/v1'

/**
 * 将后端返回的视觉资源 URL 解析为前端可直接访问的地址。
 * 后端统一返回相对路径 /v1/visual/files/{id}，与 API_BASE 一致时原样拼接；
 * 若是上游直链则保持不变。
 */
export function resolveVisualUrl(url) {
  if (!url) return url
  // 绝对路径（上游直链）直接返回
  if (/^https?:\/\//i.test(url)) return url
  // 后端规范路径：/v1/visual/files/{id}
  if (url.startsWith('/v1/visual/files/')) {
    const id = url.slice('/v1/visual/files/'.length)
    return `${API_BASE}/visual/files/${id}`
  }
  return url
}
