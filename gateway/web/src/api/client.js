import axios from 'axios'
import { getItem, setItem, removeItem, getJSON } from '../utils/storage'

const API_BASE = import.meta.env.VITE_API_BASE || '/api'

// 把后端/网络错误信息转换成普通用户能看懂的文案
function normalizeErrorMessage(status, rawMessage) {
  const msg = String(rawMessage || '')
  // 认证相关：统一提示登录
  if (status === 401 || /Authorization|Token|登录|缺少.*头|请先登录/.test(msg)) {
    return '登录已过期或无效，请重新登录'
  }
  // 上游限流或服务端限流
  if (status === 429) {
    return '服务繁忙，请稍后再试'
  }
  // 视觉生成模型未选择（兼容旧版 Gin 校验提示）
  if (/Provider|Model/.test(msg) && /required|不能为空|请选择/.test(msg)) {
    return '请选择生成模型'
  }
  return msg
}

const client = axios.create({
  baseURL: API_BASE,
  timeout: 15000,
  headers: {
    'Content-Type': 'application/json'
  }
})

// 请求拦截器：注入 JWT Token
client.interceptors.request.use(
  (config) => {
    const token = getItem('token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// 响应拦截器：剥离后端统一包装 { code, message, data }
client.interceptors.response.use(
  (response) => {
    const body = response.data
    if (body && typeof body === 'object' && 'code' in body) {
      if (body.code === 0) {
        return body.data
      }
      return Promise.reject(new Error(body.message || '请求失败'))
    }
    return body
  },
  async (error) => {
    const originalRequest = error.config
    const status = error.response?.status

    // 401 且不是刷新 token 请求本身，尝试刷新
    if (status === 401 && originalRequest && !originalRequest._retry) {
      originalRequest._retry = true
      const refreshToken = getItem('refresh_token')
      if (refreshToken) {
        try {
          const data = await authApi.refresh(refreshToken)
          if (data?.access_token) {
            setItem('token', data.access_token)
            setItem('refresh_token', data.refresh_token || refreshToken)
            originalRequest.headers.Authorization = `Bearer ${data.access_token}`
            return client(originalRequest)
          }
        } catch (refreshError) {
          // 刷新失败，清空登录态
          clearAuth()
          return Promise.reject(refreshError)
        }
      }
      clearAuth()
    }

    const rawMessage = error.response?.data?.message || error.message || '网络错误'
    const friendlyMessage = normalizeErrorMessage(status, rawMessage)
    return Promise.reject(new Error(friendlyMessage))
  }
)

function clearAuth() {
  removeItem('token')
  removeItem('refresh_token')
  removeItem('user')
}

// ====== 认证 API ======
export const authApi = {
  login: (username, password, deviceId) => client.post('/auth/login', { username, password, device_id: deviceId }),
  register: (username, password, deviceId) => client.post('/auth/register', { username, password, device_id: deviceId }),
  refresh: (refreshToken) => client.post('/auth/refresh', { refresh_token: refreshToken }),
  sendEmailOTP: (email) => client.post('/auth/email/otp/send', { email }),
  emailLogin: (email, code, deviceId) => client.post('/auth/email/login', { email, code, device_id: deviceId }),
  me: () => client.get('/auth/me')
}

// ====== 余额 API ======
export const billingApi = {
  getBalance: () => client.get('/billing/balance'),
  getRechargeHistory: (page = 1, pageSize = 20) =>
    client.get(`/billing/recharge-history?page=${page}&page_size=${pageSize}`)
}

// ====== 模型 API ======
export const modelApi = {
  list: () => client.get('/eleagent/models')
}

// ====== Ele Agent API ======
export const eleAgentApi = {
  credentials: (subProvider, subModel) =>
    client.get('/eleagent/credentials', { params: { subProvider, subModel } })
}

// ====== 充值套餐 API ======
export const rechargeApi = {
  listPackages: () => client.get('/recharge/packages')
}

// ====== 支付 API ======
export const paymentApi = {
  wechatPrepay: (userId, packageId, quantity = 1) =>
    client.post('/payment/wechat/prepay', { user_id: userId, package_id: packageId, quantity }),
  alipayOrder: (userId, packageId, quantity = 1) =>
    client.post('/payment/alipay/order', { user_id: userId, package_id: packageId, quantity }),
  // 支付宝扫码预下单（收银台二维码）。充值场景传 {package_id, quantity}；VIP 场景传 {order_id}
  alipayPrecreate: (params) => client.post('/payment/alipay/precreate', params),
  // 查询订单支付状态（收银台轮询）
  getOrderStatus: (orderId) => client.get(`/orders/${orderId}/status`)
}

// ====== VIP 会员 API ======
export const vipApi = {
  listPlans: () => client.get('/vip/plans'),
  getStatus: () => client.get('/vip/status'),
  subscribe: (planId, channel = 'wechat', useElegantBalance = false) =>
    client.post('/vip/subscribe', { plan_id: planId, channel, use_elegant_balance: useElegantBalance })
}

// ====== 兑换码 API ======
export const cdkApi = {
  redeem: (code) => client.post('/cdk/redeem', { code })
}

// ====== 公开配置 API ======
export const publicSettingApi = {
  get: () => client.get('/public/settings')
}

// ====== 对话历史 API ======
export const conversationApi = {
  list: (page = 1, pageSize = 20) =>
    client.get(`/conversations?page=${page}&page_size=${pageSize}`),
  create: (data) => client.post('/conversations', data),
  get: (id) => client.get(`/conversations/${id}`),
  update: (id, data) => client.patch(`/conversations/${id}`, data),
  delete: (id) => client.delete(`/conversations/${id}`),
  listMessages: (id, page = 1, pageSize = 50) =>
    client.get(`/conversations/${id}/messages?page=${page}&page_size=${pageSize}`),
  saveMessage: (id, data) => client.post(`/conversations/${id}/messages`, data)
}

// ====== Agent 工作流 API ======
export const agentApi = {
  listSearchProviders: () => client.get('/agent/search-providers'),
  listSessions: (page = 1, pageSize = 20) =>
    client.get(`/agent/sessions?page=${page}&page_size=${pageSize}`),
  getSession: (id) => client.get(`/agent/sessions/${id}`),
  deleteSession: (id) => client.delete(`/agent/sessions/${id}`),
  deleteAllSessions: () => client.delete('/agent/sessions'),
  deleteSessionsByConversation: (conversationId) =>
    client.delete(`/agent/sessions?conversation_id=${conversationId}`),
  getResource: (id) => `${API_BASE}/agent/resources/${id}`,
  execute: async (body, onEvent) => {
    const token = getItem('token')
    const response = await fetch(`${API_BASE}/agent/execute`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': token ? `Bearer ${token}` : ''
      },
      body: JSON.stringify(body)
    })

    if (!response.ok) {
      const text = await response.text().catch(() => '请求失败')
      throw new Error(text || `HTTP ${response.status}`)
    }

    const reader = response.body?.getReader()
    if (!reader) throw new Error('无法读取响应流')

    const decoder = new TextDecoder('utf-8')
    let buffer = ''
    try {
      while (true) {
        const { done, value } = await reader.read()
        if (done) break
        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        let currentEvent = null
        for (const line of lines) {
          const trimmed = line.trim()
          if (!trimmed) {
            currentEvent = null
            continue
          }
          if (trimmed.startsWith('event:')) {
            currentEvent = trimmed.slice(6).trim()
          } else if (trimmed.startsWith('data:') && currentEvent) {
            const data = trimmed.slice(5).trim()
            let parsed
            try {
              parsed = JSON.parse(data)
            } catch {
              parsed = data
            }
            onEvent({ event: currentEvent, data: parsed })
          }
        }
      }
    } finally {
      reader.releaseLock()
    }
  }
}

// ====== 视觉生成 API ======
// 视觉生成上游耗时较长（图片同步生成可能数十秒），单独放宽超时。
export const visualApi = {
  create: (body) => client.post('/visual/generations', body, { timeout: 120000 }),
  get: (id) => client.get(`/visual/generations/${id}`),
  cancel: (id) => client.post(`/visual/generations/${id}/cancel`),
  upload: (file) => {
    const formData = new FormData()
    formData.append('file', file)
    return client.post('/visual/upload', formData, {
      headers: { 'Content-Type': 'multipart/form-data' },
      timeout: 60000
    })
  },
  getFileUrl: (id) => `${API_BASE}/visual/files/${id}`,
  createConversation: (title, mediaType = 'image') => client.post('/visual/conversations', { title, media_type: mediaType }),
  listConversations: (page = 1, pageSize = 50, mediaType = '') =>
    client.get(`/visual/conversations?page=${page}&page_size=${pageSize}${mediaType ? `&media_type=${mediaType}` : ''}`),
  getConversation: (id) => client.get(`/visual/conversations/${id}`),
  updateConversation: (id, title) => client.patch(`/visual/conversations/${id}`, { title }),
  deleteConversation: (id) => client.delete(`/visual/conversations/${id}`)
}

export default client

// ====== Agent 市场 API ======
export const agentMarketApi = {
  // 获取当前账户能力（含 agent_market.enabled）
  getCapabilities: () => client.get('/capabilities'),
  // 分类列表
  getCategories: () => client.get('/market/categories'),
  // 秘技列表
  listAgents: (page = 1, pageSize = 20, category = '', sort = 'hot', filter = '') =>
    client.get('/agents', { params: { page, page_size: pageSize, category, sort, filter } }),
  // 秘技详情
  getAgent: (id) => client.get(`/agents/${id}`),
  // 购买秘技
  purchase: (id, currency = 'danwan') =>
    client.post(`/agents/${id}/purchase`, { agent_id: id, currency }),
  // 切换收藏
  toggleFavorite: (id) => client.post(`/agents/${id}/favorite`),
  // 切换激活状态（购买后控制是否作为工具注入 Agent 工作流）
  toggleActive: (id) => client.post(`/agents/${id}/active`),
  // 评价列表
  listReviews: (id, page = 1, pageSize = 20) =>
    client.get(`/agents/${id}/reviews`, { params: { page, page_size: pageSize } }),
  // 用户弹丸空间（用于"我的秘技"入口）
  getUserSpace: () => client.get('/space'),
  // 查询某 SKU 的凭证 schema 与当前值
  getCredentials: (id) => client.get(`/agents/${id}/credentials`),
  // 保存某 SKU 的凭证
  saveCredentials: (id, values) => client.post(`/agents/${id}/credentials`, { values })
}


// ====== SKU 凭证 API（Cookie / API Key / Token 等） ======
export const agentCredentialApi = {
  getCredentials: (id) => client.get(`/agents/${id}/credentials`),
  saveCredentials: (id, values) => client.post(`/agents/${id}/credentials`, { values })
}
