import axios from 'axios'

// ====== claw 控制台双通道 baseURL ======
// 本地 claw gateway：模块管理 / 模型配置 / 本地统计（走 /claw-console）
// 单文件二进制无反向代理，API 直连 /v1（/api 是云端 nginx 反代前缀，claw 本地不存在）
const API_BASE = import.meta.env.VITE_API_BASE || '/v1'
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
      // 认证接口自身的 401（如账密错误）不做跳转，让页面就地展示错误信息；
      // 其余接口的 401 视为会话失效，清理 token 后回登录页
      const isAuthRequest = error.config?.url?.includes('/auth/')
      if (error.response?.status === 401 && !isAuthRequest) {
        localStorage.removeItem('admin_token')
        localStorage.removeItem('admin_refresh_token')
        localStorage.removeItem('admin_user')
        // 控制台 SPA basename 为 /admin，跳服务端路径 /login 会落到 web 应用
        window.location.href = '/admin/login'
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
// 本地模块状态 + 在线情况 + token 用量（本地控制台 Dashboard 用）
export const dashboardApi = {
  // 本地已注册模块列表（含在线状态/能力/心跳），替代云端 DAU/收入统计
  getModules: () => client.get('/claw-console/modules'),
  // 本地 token 用量统计（P3 细化，替代云端 DAU/收入）
  getStats: () => client.get('/claw-console/stats'),
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
  // P4：把云端已购模块元数据安装到本地（拉镜像+签名校验+激活）
  install: (installMeta) => client.post('/claw-console/modules/install', installMeta),
  // P4：本地秘技提交云端审核（转发云端 register）
  submitForReview: (payload, authToken) =>
    client.post('/claw-console/modules/submit-review', payload, authToken ? { headers: { 'X-Module-Auth-Token': authToken } } : {}),
}

// ====== claw 云端秘技拉取（已购列表，安装到本地）======
// 契约：GET /v1/market/modules/installed -> ModuleInstallMeta[]（见 specs/api-schema.yml）
export const clawMarketApi = {
  listInstalledModules: (since) =>
    cloudClient.get('/market/modules/installed', { params: since ? { since } : {} }),
}

// ====== 本地模型配置 API（claw 本地 CRUD：BYOK + Ele Agent 云端代理，本地不计费无价格字段）======
export const eleAgentModelApi = {
  list: (page = 1, pageSize = 20) => client.get(`/claw-console/eleagent/models?page=${page}&page_size=${pageSize}`),
  create: (data) => client.post('/claw-console/eleagent/models', data),
  update: (id, data) => client.patch(`/claw-console/eleagent/models/${id}`, data),
  rotateKey: (id, apiKey) => client.post(`/claw-console/eleagent/models/${id}/rotate-key`, { api_key: apiKey }),
  remove: (id) => client.delete(`/claw-console/eleagent/models/${id}`),
  // 云端 Ele Agent 可选模型列表（快捷添加云端代理时选择 model_name；云端公开接口）
  listCloudOptions: () => cloudClient.get('/eleagent/models'),
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
