import axios from 'axios'

const API_BASE = import.meta.env.VITE_API_BASE || '/api'
const USE_MOCK = import.meta.env.VITE_USE_MOCK === 'true'

const client = axios.create({
  baseURL: API_BASE,
  timeout: 15000,
  headers: {
    'Content-Type': 'application/json'
  }
})

// Mock 数据开关：当 USE_MOCK=true 时，所有 admin API 返回本地模拟数据
if (USE_MOCK) {
  client.interceptors.request.use(async (config) => {
    const mockData = getMockData(config)
    if (mockData) {
      // 构造一个假的 axios 响应
      return Promise.resolve({
        data: { code: 0, message: 'success', data: mockData },
        status: 200,
        statusText: 'OK',
        headers: {},
        config
      })
    }
    return config
  })
}

function getMockData(config) {
  const url = config.url
  if (url === '/admin/stats') {
    return {
      total_users: 1248,
      today_active: 258,
      today_token_usage: 27900,
      today_revenue: 124000,
      total_revenue: 1542050
    }
  }
  if (url === '/admin/stats/dau') {
    return [
      { date: '05-21', value: 120 }, { date: '05-22', value: 145 },
      { date: '05-23', value: 138 }, { date: '05-24', value: 192 },
      { date: '05-25', value: 210 }, { date: '05-26', value: 235 },
      { date: '05-27', value: 258 }
    ]
  }
  if (url === '/admin/stats/token-usage') {
    return [
      { date: '05-21', value: 11700 }, { date: '05-22', value: 14300 },
      { date: '05-23', value: 13200 }, { date: '05-24', value: 20400 },
      { date: '05-25', value: 22700 }, { date: '05-26', value: 25300 },
      { date: '05-27', value: 27900 }
    ]
  }
  if (url === '/admin/users') {
    return {
      total: 1248,
      items: [
        { id: 'u_1001', username: 'alice', nickname: 'Alice', role: 'user', status: 1, balance: 5200, elegant_balance: 800, total_recharged: 10000, created_at: '2026-04-15T10:00:00Z' },
        { id: 'u_1002', username: 'bob', nickname: 'Bob', role: 'user', status: 1, balance: 1200, elegant_balance: 0, total_recharged: 5000, created_at: '2026-04-18T10:00:00Z' }
      ]
    }
  }
  if (url === '/admin/activities') {
    return [
      { id: 'act_001', type: 'user_registered', title: '新用户注册', description: '用户 bob（user_id:u_1002）注册了账户', created_at: '2026-05-27T10:30:00Z' },
      { id: 'act_002', type: 'user_recharged', title: '用户充值', description: '用户（user_id:u_1001）充值了 5000 弹丸，花费 ¥50.00', created_at: '2026-05-27T10:25:00Z' }
    ]
  }
  if (url === '/admin/billing/transactions') {
    return {
      total: 3421,
      items: [
        { id: 'tx_001', user_id: 'u_1001', type: 'consume', amount: -1200, description: 'GPT-4 对话', created_at: '2026-05-27T10:23:00Z' },
        { id: 'tx_002', user_id: 'u_1002', type: 'recharge', amount: 5000, description: '微信支付', created_at: '2026-05-27T09:56:00Z' }
      ]
    }
  }
  if (url === '/admin/orders') {
    return {
      total: 856,
      items: [
        { id: 'ord_001', user_id: 'u_1001', channel: 'wechat', amount: 9900, status: 'paid', created_at: '2026-05-27T09:56:00Z' },
        { id: 'ord_002', user_id: 'u_1002', channel: 'alipay', amount: 5000, status: 'paid', created_at: '2026-05-26T14:30:00Z' }
      ]
    }
  }
  return null
}

// 请求拦截器：注入 JWT Token
client.interceptors.request.use(
  (config) => {
    const token = localStorage.getItem('admin_token')
    if (token) {
      config.headers.Authorization = `Bearer ${token}`
    }
    return config
  },
  (error) => Promise.reject(error)
)

// 响应拦截器：剥离外层 {code, message, data} 包装
client.interceptors.response.use(
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
      window.location.href = '/admin/login'
    }
    return Promise.reject(error.response?.data?.message || error.message)
  }
)

// ====== 认证 API ======
export const authApi = {
  login: (username, password) => client.post('/auth/login', { username, password, device_id: 'admin-web' }),
  register: (username, password) => client.post('/auth/register', { username, password, device_id: 'admin-web' }),
  refresh: (refreshToken) => client.post('/auth/refresh', { refresh_token: refreshToken })
}

// ====== Dashboard / 统计 API ======
export const dashboardApi = {
  getStats: () => client.get('/admin/stats'),
  getDailyActive: (days = 7) => client.get(`/admin/stats/dau?days=${days}`),
  getTokenUsage: (days = 7) => client.get(`/admin/stats/token-usage?days=${days}`),
  getActivities: (limit = 20) => client.get(`/admin/activities?limit=${limit}`)
}

// ====== 用户管理 API ======
export const userApi = {
  list: (page = 1, pageSize = 20, search = '', status) => {
    let url = `/admin/users?page=${page}&page_size=${pageSize}`
    if (search) url += `&search=${encodeURIComponent(search)}`
    if (status !== undefined && status !== null && status !== '') url += `&status=${status}`
    return client.get(url)
  },
  detail: (id) => client.get(`/admin/users/${id}`),
  updateStatus: (id, status) => client.patch(`/admin/users/${id}/status`, { status }),
  delete: (id) => client.delete(`/admin/users/${id}`)
}

// ====== 计费 API ======
// 后端路由：/v1/admin/billing/transactions、/v1/admin/billing/recharge
export const billingApi = {
  listTransactions: (page = 1, pageSize = 20, type) => {
    let url = `/admin/billing/transactions?page=${page}&page_size=${pageSize}`
    if (type) url += `&type=${type}`
    return client.get(url)
  },
  getUserBalance: (userId) => client.get(`/admin/billing/users/${userId}/balance`),
  recharge: (userId, amount, currency = 'danwan') => client.post('/admin/billing/recharge', { user_id: userId, amount, currency })
}

// ====== 订单 API ======
export const orderApi = {
  list: (page = 1, pageSize = 20, status) => {
    let url = `/admin/orders?page=${page}&page_size=${pageSize}`
    if (status) url += `&status=${status}`
    return client.get(url)
  },
  detail: (id) => client.get(`/admin/orders/${id}`),
  refund: (id) => client.post(`/admin/orders/${id}/refund`),
  confirm: (id) => client.post(`/admin/orders/${id}/confirm`)
}

// ====== 秘技审核 API ======
export const agentApi = {
  listForReview: (page = 1, pageSize = 20, status = '') => {
    let url = `/admin/agents?page=${page}&page_size=${pageSize}`
    if (status) url += `&status=${status}`
    return client.get(url)
  },
  dependencies: (id) => client.get(`/admin/agents/${id}/dependencies`),
  approve: (id, adminNote = '') => client.post(`/admin/agents/${id}/approve`, { status: 'approved', admin_note: adminNote }),
  reject: (id, adminNote = '') => client.post(`/admin/agents/${id}/reject`, { status: 'rejected', admin_note: adminNote }),
  delist: (id) => client.post(`/admin/agents/${id}/delist`)
}

// ====== EleAgent 模型配置 API ======
export const eleAgentModelApi = {
  list: (page = 1, pageSize = 100) => client.get(`/admin/eleagent/models?page=${page}&page_size=${pageSize}`)
}

// ====== 系统设置 API ======
export const settingsApi = {
  get: () => client.get('/admin/settings'),
  update: (settings) => client.put('/admin/settings', settings)
}

// ====== 充值套餐 API ======
export const rechargePackageApi = {
  list: () => client.get('/admin/recharge/packages'),
  create: (data) => client.post('/admin/recharge/packages', data),
  update: (id, data) => client.patch(`/admin/recharge/packages/${id}`, data),
  delete: (id) => client.delete(`/admin/recharge/packages/${id}`)
}

// ====== VIP 会员 API ======
export const vipApi = {
  listPlans: () => client.get('/admin/vip/plans'),
  createPlan: (data) => client.post('/admin/vip/plans', data),
  updatePlan: (id, data) => client.patch(`/admin/vip/plans/${id}`, data),
  deletePlan: (id) => client.delete(`/admin/vip/plans/${id}`),
  listSubscriptions: (page = 1, pageSize = 20, userId = '') => {
    let url = `/admin/vip/subscriptions?page=${page}&page_size=${pageSize}`
    if (userId) url += `&user_id=${encodeURIComponent(userId)}`
    return client.get(url)
  },
  grant: (userId, planId, months) => client.post(`/admin/users/${userId}/vip`, { plan_id: planId, months })
}

// ====== 兑换码 API ======
export const cdkApi = {
  batchGenerate: (value, count, note = '') => client.post('/admin/cdk/batch', { value, count, note }),
  list: (params = {}) => client.get('/admin/cdk', { params }),
  delete: (id) => client.delete(`/admin/cdk/${id}`)
}

// ====== 集市模块 / 动态驱动 API ======
export const moduleApi = {
  listModules: () => client.get('/admin/modules'),
  registerModule: (data) => client.post('/admin/modules', data),
  deleteModule: (id) => client.delete(`/admin/modules/${id}`),
  refreshModule: (id) => client.post(`/admin/modules/${id}/refresh`),
  rescanMarketplace: () => client.post('/admin/modules/rescan'),
  listDrivers: () => client.get('/admin/drivers'),
  registerDriver: (data) => client.post('/admin/drivers', data),
  deleteDriver: (id) => client.delete(`/admin/drivers/${id}`)
}

export default client
