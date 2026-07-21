import axios from 'axios'

// ====== claw 控制台双通道 baseURL ======
// 本地 claw gateway：模块管理 / 模型配置 / 本地统计（走 /claw-console）
const API_BASE = import.meta.env.VITE_API_BASE || '/api'
// 云端 eleball：账户登录 / 当前用户计费（统一账户，与 web/APP 同体系）
const CLOUD_API = import.meta.env.VITE_CLOUD_API || 'https://api.eleball.cn/v1'

// 本地实例（claw 控制台管理面 + 本地数据）
const client = axios.create({
  baseURL: API_BASE,
  timeout: 15000,
  headers: { 'Content-Type': 'application/json' }
})

// 云端实例（账户 + 当前用户计费）
const cloudClient = axios.create({
  baseURL: CLOUD_API,
  timeout: 15000,
  headers: { 'Content-Type': 'application/json' }
})

// 统一注入 admin_token（与云端签发的 JWT 通用，需 claw JWT_SECRET 与云端一致）
function attachToken(config) {
  const token = localStorage.getItem('admin_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
}

client.interceptors.request.use(attachToken, (e) => Promise.reject(e))
cloudClient.interceptors.request.use(attachToken, (e) => Promise.reject(e))

// 响应拦截器：剥离外层 {code, message, data}
function responseInterceptor(instance) {
  instance.interceptors.response.use(
    (response) => {
      const body = response.data
      if (body && typeof body === 'object' && 'code' in body) {
        if (body.code === 0) {
          return body.data
        }
        return Promise.reject(body.message || '请求失败')
      }
      return body
    },
    (error) => {
      if (error.response?.status === 401) {
        localStorage.removeItem('admin_token')
        localStorage.removeItem('admin_refresh_token')
        localStorage.removeItem('admin_user')
        window.location.href = '/login'
      }
      return Promise.reject(error.response?.data?.message || error.message)
    }
  )
}
responseInterceptor(client)
responseInterceptor(cloudClient)

// ====== 认证 API（云端统一账户）======
export const authApi = {
  login: (username, password) => cloudClient.post('/auth/login', { username, password, device_id: 'claw-console' }),
  register: (username, password) => cloudClient.post('/auth/register', { username, password, device_id: 'claw-console' }),
  refresh: (refreshToken) => cloudClient.post('/auth/refresh', { refresh_token: refreshToken })
}

// ====== 本地控制台 API（claw /claw-console）======
// 本地模块状态 + 在线情况（本地控制台 Dashboard 用）
export const dashboardApi = {
  // 本地已注册模块列表（含在线状态/能力/心跳），替代云端 DAU/收入统计
  getModules: () => client.get('/claw-console/modules'),
  // 本地动态（Agent 运行记录等，后端按需实现；当前可复用模块心跳）
  getActivities: () => client.get('/claw-console/modules').then((d) => d?.items || d || []),
}

// ====== 本地集市模块 / 动态驱动 API（claw 本地，复用 ModuleHandler）======
export const moduleApi = {
  listModules: () => client.get('/claw-console/modules'),
  registerModule: (data) => client.post('/claw-console/modules', data),
  deleteModule: (id) => client.delete(`/claw-console/modules/${id}`),
  refreshModule: (id) => client.post(`/claw-console/modules/${id}/refresh`),
  rescanMarketplace: () => client.post('/claw-console/modules/rescan'),
  listDrivers: () => client.get('/claw-console/drivers'),
  registerDriver: (data) => client.post('/claw-console/drivers', data),
  deleteDriver: (id) => client.delete(`/claw-console/drivers/${id}`),
  // 提交本地秘技到云端审核（转发云端 register 接口，需 auth_token）
  submitForReview: (payload) => cloudClient.post('/market/modules/register', payload),
}

// ====== 本地模型配置 API（claw 本地，改名"模型配置"，无调用价格）======
export const eleAgentModelApi = {
  list: (page = 1, pageSize = 100) => client.get(`/claw-console/eleagent/models?page=${page}&page_size=${pageSize}`)
}

// ====== 本地系统设置 API ======
export const settingsApi = {
  get: () => client.get('/claw-console/settings'),
  update: (settings) => client.put('/claw-console/settings', settings)
}

// ====== 当前用户计费 API（云端，只看自己；去平台总收入）======
// claw 本地不计费；展示云端账户弹丸余额与记录（用户在云端充值/扣费）
export const billingApi = {
  getBalance: () => cloudClient.get('/billing/balance'),
  getRechargeHistory: (page = 1, pageSize = 20) =>
    cloudClient.get(`/billing/recharge-history?page=${page}&page_size=${pageSize}`)
}

export default client
