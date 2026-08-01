import axios from 'axios'
import { getItem, setItem, removeItem, getJSON } from '../utils/storage'

// ====== claw 双通道 baseURL ======
// 本地 claw gateway：对话/视觉/模型/技能/Agent 工作流/对话历史/同步/STT/本地模块
// 单文件二进制无反向代理，API 直连 /v1（/api 是云端 nginx 反代前缀，claw 本地不存在）
const API_BASE = import.meta.env.VITE_API_BASE || '/v1'
// 云端 eleball：账户（登录/注册/邮箱OTP/刷新/我的）/充值/支付/VIP/CDK/秘技购买/已购秘技拉取
const CLOUD_API = import.meta.env.VITE_CLOUD_API || 'https://api.eleball.cn/v1'
// 云端 web：官网/充值内嵌用（CLOUD_BASE，作为 CloudFrame 的 iframe src）
export const CLOUD_BASE = import.meta.env.VITE_CLOUD_BASE || 'https://www.eleball.cn'

// 把后端/网络错误信息转换成普通用户能看懂的文案
function normalizeErrorMessage(status, rawMessage) {
  const msg = String(rawMessage || '')
  if (status === 401 || /Authorization|Token|登录|缺少.*头|请先登录/.test(msg)) {
    return '登录已过期或无效，请重新登录'
  }
  if (status === 429) {
    return '服务繁忙，请稍后再试'
  }
  if (/Provider|Model/.test(msg) && /required|不能为空|请选择/.test(msg)) {
    return '请选择生成模型'
  }
  return msg
}

function clearAuth() {
  removeItem('token')
  removeItem('refresh_token')
  removeItem('user')
}

// 云端 token 刷新（统一账户：claw 与云端共享 JWT secret，一个 token 两端通用；
// 刷新统一走云端 /auth/refresh，避免在双 client 上递归触发拦截器）
async function refreshTokenCloud(refreshToken) {
  try {
    const resp = await axios.post(
      `${CLOUD_API}/auth/refresh`,
      { refresh_token: refreshToken },
      { headers: { 'Content-Type': 'application/json' } }
    )
    const body = resp.data
    if (body && body.code === 0) return body.data
    return null
  } catch {
    return null
  }
}

// 创建带统一拦截器的 axios 实例：注入 JWT、剥离 { code, message, data }、401 自动刷新。
// baseURL 决定该实例指向本地 claw 还是云端 eleball。
function createClient(baseURL, { timeout = 15000 } = {}) {
  const c = axios.create({
    baseURL,
    timeout,
    headers: { 'Content-Type': 'application/json' }
  })

  c.interceptors.request.use(
    (config) => {
      const token = getItem('token')
      if (token) {
        config.headers.Authorization = `Bearer ${token}`
      }
      return config
    },
    (error) => Promise.reject(error)
  )

  c.interceptors.response.use(
    (response) => {
      const body = response.data
      if (body && typeof body === 'object' && 'code' in body) {
        if (body.code === 0) {
          return body.data
        }
        // 透传业务错误码（如 4002=VIP 门禁），页面可据此做引导文案
        const bizErr = new Error(body.message || '请求失败')
        bizErr.code = body.code
        return Promise.reject(bizErr)
      }
      return body
    },
    async (error) => {
      const originalRequest = error.config
      const status = error.response?.status

      // 401 且不是刷新请求本身，尝试刷新（统一走云端）
      if (status === 401 && originalRequest && !originalRequest._retry) {
        originalRequest._retry = true
        const refreshToken = getItem('refresh_token')
        if (refreshToken) {
          try {
            const data = await refreshTokenCloud(refreshToken)
            if (data?.access_token) {
              setItem('token', data.access_token)
              setItem('refresh_token', data.refresh_token || refreshToken)
              // 通知 AuthContext 同步新 token 到内嵌云端官网 iframe（refresh_token 会轮换，需同步）
              window.dispatchEvent(new CustomEvent('eleball:auth-refreshed', {
                detail: { access_token: data.access_token, refresh_token: data.refresh_token || refreshToken }
              }))
              originalRequest.headers.Authorization = `Bearer ${data.access_token}`
              return c(originalRequest)
            }
          } catch (refreshError) {
            clearAuth()
            return Promise.reject(refreshError)
          }
        }
        clearAuth()
      }

      const rawMessage = error.response?.data?.message || error.message || '网络错误'
      const friendlyMessage = normalizeErrorMessage(status, rawMessage)
      // 同样透传业务错误码（HTTP 4xx/5xx 场景，如 VIP 门禁 403+4002）
      const httpErr = new Error(friendlyMessage)
      httpErr.code = error.response?.data?.code
      return Promise.reject(httpErr)
    }
  )

  return c
}

// 本地 claw gateway 实例
const client = createClient(API_BASE)
// 云端 eleball 实例（账户/充值/秘技购买/已购拉取）
const cloudClient = createClient(CLOUD_API)

// ====== 认证 API（云端统一账户）======
export const authApi = {
  // 登录：identifier 含 @ 走邮箱登录，否则用户名登录
  login: (identifier, password, deviceId) =>
    cloudClient.post(
      '/auth/login',
      identifier.includes('@')
        ? { email: identifier, password, device_id: deviceId }
        : { username: identifier, password, device_id: deviceId }
    ),
  // 注册：用户名 + 密码 + 邮箱 + 邮箱验证码
  register: (username, password, email, code, deviceId) =>
    cloudClient.post('/auth/register', { username, password, email, code, device_id: deviceId }),
  refresh: (refreshToken) => cloudClient.post('/auth/refresh', { refresh_token: refreshToken }),
  // 发送邮箱验证码（注册邮箱验证 / 密码找回）
  sendEmailOTP: (email) => cloudClient.post('/auth/email/otp/send', { email }),
  me: () => cloudClient.get('/auth/me')
}

// ====== 余额 API（云端账户余额；claw 本地不计费，余额即云端弹丸）======
export const billingApi = {
  getBalance: () => cloudClient.get('/billing/balance'),
  getRechargeHistory: (page = 1, pageSize = 20) =>
    cloudClient.get(`/billing/recharge-history?page=${page}&page_size=${pageSize}`)
}

// ====== 模型 API（本地 claw：展示本地化模型配置，非云端获取）======
export const modelApi = {
  list: () => client.get('/eleagent/models'),
  // 云端 Ele Agent 模型列表（Ele Agent 代理模型的云端计费价格来源；公开接口）
  listCloud: () => cloudClient.get('/eleagent/models'),
}

// ====== Ele Agent API（本地 claw 凭证）======
export const eleAgentApi = {
  credentials: (subProvider, subModel) =>
    client.get('/eleagent/credentials', { params: { subProvider, subModel } })
}

// ====== 充值套餐 API（云端）======
export const rechargeApi = {
  listPackages: () => cloudClient.get('/recharge/packages')
}

// ====== 支付 API（云端）======
export const paymentApi = {
  wechatPrepay: (userId, packageId, quantity = 1) =>
    cloudClient.post('/payment/wechat/prepay', { user_id: userId, package_id: packageId, quantity }),
  alipayOrder: (userId, packageId, quantity = 1) =>
    cloudClient.post('/payment/alipay/order', { user_id: userId, package_id: packageId, quantity }),
  alipayPrecreate: (params) => cloudClient.post('/payment/alipay/precreate', params),
  getOrderStatus: (orderId) => cloudClient.get(`/orders/${orderId}/status`)
}

// ====== VIP 会员 API（云端）======
export const vipApi = {
  listPlans: () => cloudClient.get('/vip/plans'),
  getStatus: () => cloudClient.get('/vip/status'),
  subscribe: (planId, channel = 'wechat', useElegantBalance = false) =>
    cloudClient.post('/vip/subscribe', { plan_id: planId, channel, use_elegant_balance: useElegantBalance })
}

// ====== 兑换码 API（云端）======
export const cdkApi = {
  redeem: (code) => cloudClient.post('/cdk/redeem', { code })
}

// ====== 公开配置 API（本地 claw）======
export const publicSettingApi = {
  get: () => client.get('/public/settings')
}

// ====== 系统状态 API（本地 claw：Docker/Compose 可用性等，用于缺失引导横幅）======
export const systemApi = {
  // -> { docker_available, docker_version, compose_available, modules_auto_start, modules_auto_stop }
  status: () => client.get('/claw-console/system/status')
}

// ====== 工作目录 API（本地 claw：AR-06，DirectoryPicker 消费）======
export const cwdApi = {
  // 列出目录条目（path 空默认用户主目录）-> { path, entries: [{name,is_dir,size,modified}] }
  browse: (path = '') => client.get('/claw-console/cwd/browse', { params: { path } }),
  // 校验路径为目录 -> { cwd, path }
  validate: (path) => client.post('/claw-console/cwd/validate', { path })
}

// ====== 文件浏览器/预览 API（本地 claw：AR-11，FileExplorer/FileViewer 消费）======
export const clawFilesApi = {
  // 列出 cwd 下条目 -> { path, entries: [{name,is_dir,size,modified}] }
  list: (cwd, path = '.') =>
    client.get('/claw-console/files', { params: { cwd, path, type: 'list' } }),
  // 下载文件内容（返回 Blob，带 JWT）-> Blob
  fetch: (cwd, path) =>
    client.get('/claw-console/files', { params: { cwd, path, type: 'download' }, responseType: 'blob' }),
  // 查询 Git 状态 -> { is_repo, branch, ahead, behind, clean, entries: [{path,x,y,status}] }
  gitStatus: (cwd) => client.get('/claw-console/git/status', { params: { cwd } }),
  // AR-21：新建目录（body {cwd, path}）-> { path }
  createDir: (cwd, path) => client.post('/claw-console/files/mkdir', { cwd, path }),
  // AR-21：移动/重命名（body {cwd, src_path, dst_path}）-> { path }
  move: (cwd, srcPath, dstPath) => client.post('/claw-console/files/move', { cwd, src_path: srcPath, dst_path: dstPath }),
  // AR-21：删除文件或目录（body {cwd, path}）-> { path }
  remove: (cwd, path) => client.delete('/claw-console/files', { data: { cwd, path } })
}

// ====== Worktree 切换 API（本地 claw：AR-17 O16，WorktreeSwitcher 消费）======
export const worktreeApi = {
  // 列出 cwd 所属项目根 + 全部 worktree
  // -> { projectRoot, isGit, isTopLevel, worktrees: [{path,branch,isMain}] }
  list: (cwd) => client.get('/claw-console/worktrees', { params: { cwd } }),
  // 创建 worktree（body {cwd,branch}）-> { path, branch }
  create: (cwd, branch) => client.post('/claw-console/worktrees', { cwd, branch }),
  // 删除 worktree（body {cwd,path,force}）-> { dirty }；dirty=true 表示有未提交改动需 force 二次确认
  remove: (cwd, path, force = false) =>
    client.delete('/claw-console/worktrees', { data: { cwd, path, force } })
}


// ====== 对话历史 API（本地 claw：本地存储）======
export const conversationApi = {
  // teamId 非空时按组过滤；空串 = 全部对话
  list: (page = 1, pageSize = 20, teamId = '') =>
    client.get(`/conversations?page=${page}&page_size=${pageSize}${teamId ? `&team_id=${encodeURIComponent(teamId)}` : ''}`),
  create: (data) => client.post('/conversations', data),
  get: (id) => client.get(`/conversations/${id}`),
  update: (id, data) => client.patch(`/conversations/${id}`, data),
  delete: (id) => client.delete(`/conversations/${id}`),
  listMessages: (id, page = 1, pageSize = 50) =>
    client.get(`/conversations/${id}/messages?page=${page}&page_size=${pageSize}`),
  saveMessage: (id, data) => client.post(`/conversations/${id}/messages`, data)
}

// ====== 对话分组 API（Agent Team；组严格按 user_id 隔离）======
// 组内对话共享记忆，组内助手可被编排者经 CallAssistant 委派。
export const teamApi = {
  list: () => client.get('/teams'),                          // -> [{ id, name, description, conversation_count, ... }]
  create: (data) => client.post('/teams', data),             // { name, description? }
  get: (id) => client.get(`/teams/${id}`),                   // -> { ...team, conversations: [...] }
  update: (id, data) => client.patch(`/teams/${id}`, data),  // { name?, description? }
  remove: (id) => client.delete(`/teams/${id}`),
  // 组共享记忆（scope = user + team）
  listMemories: (id, page = 1, pageSize = 20) =>
    client.get(`/teams/${id}/memories?page=${page}&page_size=${pageSize}`), // -> { items, total }
  createMemory: (id, data) => client.post(`/teams/${id}/memories`, data),   // { content, tags? }
  removeMemory: (teamId, memoryId) => client.delete(`/teams/${teamId}/memories/${memoryId}`)
}

// ====== Agent 工作流 API（本地 claw）======
export const agentApi = {
  listSearchProviders: () => client.get('/agent/search-providers'),
  listSessions: (page = 1, pageSize = 20) =>
    client.get(`/agent/sessions?page=${page}&page_size=${pageSize}`),
  getSession: (id) => client.get(`/agent/sessions/${id}`),
  deleteSession: (id) => client.delete(`/agent/sessions/${id}`),
  deleteAllSessions: () => client.delete('/agent/sessions'),
  deleteSessionsByConversation: (conversationId) =>
    client.delete(`/agent/sessions?conversation_id=${conversationId}`),
  // AR-12 会话分叉：从分叉点消息 entryId 复制父 session 对话历史到新 session
  forkSession: (id, entryId) =>
    client.post(`/agent/sessions/${id}/fork`, { entry_id: entryId }),
  getResource: (id) => `${API_BASE}/agent/resources/${id}`,
  // C1 工具审批：跨 HTTP 请求解锁阻塞的工具循环（决策投递到等待中的 channel）
  approveToolCall: (sessionId, toolCallId, decision, alwaysAllow) =>
    client.post('/agent/approve', {
      session_id: sessionId,
      tool_call_id: toolCallId,
      decision,
      always_allow: alwaysAllow || undefined
    }),
  // C1 权限规则管理（「总是允许/拒绝」Tool(spec) 规则）
  listPermissionRules: () => client.get('/agent/permission-rules'),
  addPermissionRule: (spec, decision) => client.post('/agent/permission-rules', { spec, decision }),
  deletePermissionRule: (spec) => client.delete('/agent/permission-rules', { params: { spec } }),
  // C3 plan 审批决策（ExitPlanMode 阻塞，接受/拒绝/细化 + 反馈）
  submitPlanReview: (sessionId, toolCallId, decision, feedback) =>
    client.post('/agent/plan-review', {
      session_id: sessionId,
      tool_call_id: toolCallId,
      decision,
      feedback: feedback || undefined
    }),
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

// ====== 助手 API（本地 claw：已激活秘技的命名组合，按会话应用）======
export const assistantApi = {
  list: () => client.get('/assistants'),
  create: (data) => client.post('/assistants', data),
  get: (id) => client.get(`/assistants/${id}`),
  update: (id, data) => client.patch(`/assistants/${id}`, data),
  remove: (id) => client.delete(`/assistants/${id}`),
  // 全量设置助手包含的秘技（仅允许已购+已激活的秘技，否则后端返回 3001）
  setItems: (id, agentIds) => client.put(`/assistants/${id}/items`, { agent_ids: agentIds })
}

// ====== 视觉生成 API（本地 claw）======
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

// ====== Agent 市场 API（本地 + 云端混合）======
// 本地部分（claw 本地模块/驱动 + 云端已购合并展示）走 client；
// 云端部分（购买/我的空间/能力/已购拉取）走 cloudClient。
export const agentMarketApi = {
  // --- 本地 claw ---
  getCategories: () => client.get('/market/categories'),
  // 秘技列表：claw 本地（本地自部署模块 + 已安装的云端模块），登录态由页面再合并云端已购
  listAgents: (page = 1, pageSize = 20, category = '', sort = 'hot', filter = '') =>
    client.get('/agents', { params: { page, page_size: pageSize, category, sort, filter } }),
  getAgent: (id) => client.get(`/agents/${id}`),
  // 切换收藏 / 激活（本地运行态）
  toggleFavorite: (id) => client.post(`/agents/${id}/favorite`),
  toggleActive: (id) => client.post(`/agents/${id}/active`),
  // 评价（本地）
  listReviews: (id, page = 1, pageSize = 20) =>
    client.get(`/agents/${id}/reviews`, { params: { page, page_size: pageSize } }),
  createReview: (id, rating, comment) =>
    client.post(`/agents/${id}/reviews`, { rating, comment }),
  // SKU 凭证（本地）
  getCredentials: (id) => client.get(`/agents/${id}/credentials`),
  saveCredentials: (id, values) => client.post(`/agents/${id}/credentials`, { values }),
  // 提交本地秘技到云端审核（转发云端 register 接口）
  submitForReview: (payload) => cloudClient.post('/market/modules/register', payload),
  // 本地购买：仅免费 SKU 可成功，付费 SKU 由后端返回「付费秘技请到云端购买」
  purchaseLocal: (id, currency = 'danwan') =>
    client.post(`/agents/${id}/purchase`, { agent_id: id, currency }),

  // --- 云端 eleball ---
  // 购买秘技（云端账户扣费）
  purchase: (id, currency = 'danwan') =>
    cloudClient.post(`/agents/${id}/purchase`, { agent_id: id, currency }),
  // 用户弹丸空间（云端账户）
  getUserSpace: () => cloudClient.get('/space'),
  // 账户能力（含 agent_market.enabled，云端门控）
  getCapabilities: () => cloudClient.get('/capabilities')
}

// ====== claw 云端秘技拉取（claw 登录态拉已购秘技元数据，安装到本地）======
// 契约：GET /v1/market/modules/installed -> ModuleInstallMeta[]（见 specs/api-schema.yml）
export const clawMarketApi = {
  listInstalledModules: (since) =>
    cloudClient.get('/market/modules/installed', { params: since ? { since } : {} }),
  // 安装云端已购秘技到本地（body 为单个 ModuleInstallMeta）；
  // official=false 时后端做 VIP1+ 门禁，未达标返回 code=4002（HTTP 403）
  installModule: (meta) => client.post('/claw-console/modules/install', meta)
}

// ====== SKU 凭证 API（本地 claw，Cookie / API Key / Token）======
export const agentCredentialApi = {
  getCredentials: (id) => client.get(`/agents/${id}/credentials`),
  saveCredentials: (id, values) => client.post(`/agents/${id}/credentials`, { values })
}
