import { test, expect } from '@playwright/test'

/**
 * Agent Market（秘技集市）E2E 测试
 * 默认读取环境变量：
 *   E2E_BASE_URL=http://localhost:5174
 *   E2E_API_URL=http://localhost:8080
 */

const apiBase = process.env.E2E_API_URL || 'http://localhost:8080'
const testUser = {
  username: 'e2e_ui_market_user@eleball.app',
  password: 'e2e_password',
  device_id: 'e2e-ui-market-device',
}

async function isBackendReachable(request) {
  try {
    const ping = await request.get(`${apiBase}/health`, { timeout: 3000 })
    return ping.status() === 200
  } catch (e) {
    return false
  }
}

async function ensureTestAccount(request) {
  try {
    await request.post(`${apiBase}/v1/auth/register`, { data: testUser })
  } catch (e) {
    // 已存在或后端未启动均忽略
  }
}

async function loginAndSetStorage(page, request, user = testUser) {
  await ensureTestAccount(request)
  const resp = await request.post(`${apiBase}/v1/auth/login`, { data: user })
  if (resp.status() !== 200) {
    throw new Error('登录失败，请确认后端已启动且测试账号可用')
  }
  const { data } = await resp.json()

  const meResp = await request.get(`${apiBase}/v1/auth/me`, {
    headers: { Authorization: `Bearer ${data.access_token}` },
  })
  const me = meResp.status() === 200 ? (await meResp.json()).data : { user_id: data.user_id, username: user.username, role: 'user' }

  await page.goto('/agents')
  await page.evaluate(({ token, user }) => {
    localStorage.setItem('eleball_token', token)
    localStorage.setItem('eleball_user', JSON.stringify(user))
  }, { token: data.access_token, user: me })
}

test.describe('秘技集市页面', () => {
  test('已登录用户可见「全部秘技 / 我的秘技」Tab 并可切换', async ({ page, request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过 Agent Market E2E 测试')

    await loginAndSetStorage(page, request)
    await page.reload()

    // 默认显示「秘技集市」
    await expect(page.getByRole('heading', { name: '秘技集市' })).toBeVisible()

    // 两个 Tab 都存在
    const allTab = page.getByRole('button', { name: '全部秘技' })
    const ownedTab = page.getByRole('button', { name: '我的秘技' })
    await expect(allTab).toBeVisible()
    await expect(ownedTab).toBeVisible()

    // 切换到「我的秘技」
    await ownedTab.click()
    await expect(page.getByRole('heading', { name: '我的秘技' })).toBeVisible()
  })

  test('领取免费秘技后，filter=owned 接口返回该秘技且已激活置顶', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过 Agent Market API E2E 测试')

    await ensureTestAccount(request)
    const loginResp = await request.post(`${apiBase}/v1/auth/login`, { data: testUser })
    expect(loginResp.status()).toBe(200)
    const { data } = await loginResp.json()
    const token = data?.access_token

    // 1. 先获取列表，找一个免费秘技
    const listResp = await request.get(`${apiBase}/v1/agents`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(listResp.status()).toBe(200)
    const listData = await listResp.json()
    const freeAgent = listData.data.items.find((a) => a.price_danwan === 0)
    expect(freeAgent).toBeDefined()

    // 2. 领取该秘技
    const purchaseResp = await request.post(`${apiBase}/v1/agents/${freeAgent.id}/purchase`, {
      headers: { Authorization: `Bearer ${token}` },
      data: { agent_id: freeAgent.id, currency: 'danwan' },
    })
    expect(purchaseResp.status()).toBe(200)

    // 3. 调用 filter=owned，验证返回结果包含刚领取的秘技且 is_active=true
    const ownedResp = await request.get(`${apiBase}/v1/agents?filter=owned`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(ownedResp.status()).toBe(200)
    const ownedData = await ownedResp.json()
    const ownedItems = ownedData.data.items
    const ownedAgent = ownedItems.find((a) => a.id === freeAgent.id)
    expect(ownedAgent).toBeDefined()
    expect(ownedAgent.is_active).toBe(true)

    // 4. 验证已激活秘技排在列表最前面
    if (ownedItems.length > 1) {
      expect(ownedItems[0].is_active).toBe(true)
    }
  })
})


const adminUser = {
  username: 'admin',
  password: 'admin123',
  device_id: 'e2e-admin-device',
}

async function loginUser(request) {
  await ensureTestAccount(request)
  const resp = await request.post(`${apiBase}/v1/auth/login`, { data: testUser })
  expect(resp.status()).toBe(200)
  const { data } = await resp.json()
  return data.access_token
}

async function loginAdmin(request) {
  const resp = await request.post(`${apiBase}/v1/auth/login`, { data: adminUser })
  expect(resp.status()).toBe(200)
  const { data } = await resp.json()
  return data.access_token
}

async function getCapabilitiesModules(request, token) {
  const resp = await request.get(`${apiBase}/v1/capabilities`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(resp.status()).toBe(200)
  const { data } = await resp.json()
  return data.modules || []
}

async function listAgents(request, token) {
  const resp = await request.get(`${apiBase}/v1/agents`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  expect(resp.status()).toBe(200)
  const { data } = await resp.json()
  return data.items || []
}

test.describe.configure({ mode: 'serial' })

test.describe('集市模块动态注册与离线过滤', () => {
  test('插件可通过自注册接口上报新模块，用户 capabilities 中可见', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过模块 E2E 测试')

    const moduleID = `e2e-test-module-${Date.now()}`
    const registerResp = await request.post(`${apiBase}/v1/market/modules/register`, {
      headers: { 'X-Module-Auth-Token': 'e2e-test-token' },
      data: {
        module_id: moduleID,
        name: 'E2E 测试模块',
        description: '用于 E2E 测试的临时模块',
        url: `http://${moduleID}:8080`,
        transport_type: 'module',
        capabilities: ['test_action'],
        version: '0.0.1',
      },
    })
    expect(registerResp.status()).toBe(200)

    const token = await loginUser(request)
    const modules = await getCapabilitiesModules(request, token)
    const found = modules.find((m) => m.module_id === moduleID)
    expect(found).toBeDefined()
    expect(found.online).toBe(false)

    // 清理：管理员删除该测试模块
    const adminToken = await loginAdmin(request)
    const delResp = await request.delete(`${apiBase}/v1/admin/modules/${moduleID}`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(delResp.status()).toBe(200)
  })

  test('管理员刷新后，离线模块变为在线并出现在 capabilities 中', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过模块 E2E 测试')

    const moduleID = `e2e-refresh-module-${Date.now()}`
    const adminToken = await loginAdmin(request)

    // 先以离线状态注册一个测试模块
    const regResp = await request.post(`${apiBase}/v1/admin/modules`, {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: {
        module_id: moduleID,
        name: 'E2E 刷新测试模块',
        url: `http://${moduleID}:8080`,
        transport_type: 'module',
        capabilities: ['demo'],
        version: '0.0.1',
      },
    })
    expect(regResp.status()).toBe(200)

    // 刷新后应变为在线
    const refreshResp = await request.post(`${apiBase}/v1/admin/modules/${moduleID}/refresh`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(refreshResp.status()).toBe(200)

    const token = await loginUser(request)
    const modules = await getCapabilitiesModules(request, token)
    const found = modules.find((m) => m.module_id === moduleID)
    expect(found).toBeDefined()
    expect(found.online).toBe(true)

    // 清理
    const delResp = await request.delete(`${apiBase}/v1/admin/modules/${moduleID}`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(delResp.status()).toBe(200)
  })

  test('管理员可删除已注册模块，capabilities 中不再返回', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过模块 E2E 测试')

    const moduleID = `e2e-delete-module-${Date.now()}`
    const adminToken = await loginAdmin(request)

    // 先由管理员注册
    const regResp = await request.post(`${apiBase}/v1/admin/modules`, {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: {
        module_id: moduleID,
        name: '待删除模块',
        url: `http://${moduleID}:8080`,
        transport_type: 'module',
        capabilities: ['demo'],
        version: '1.0.0',
      },
    })
    expect(regResp.status()).toBe(200)

    // 删除
    const delResp = await request.delete(`${apiBase}/v1/admin/modules/${moduleID}`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(delResp.status()).toBe(200)

    // 用户 capabilities 中不应再包含该模块
    const token = await loginUser(request)
    const modules = await getCapabilitiesModules(request, token)
    const found = modules.find((m) => m.module_id === moduleID)
    expect(found).toBeUndefined()
  })

  test('模块离线时，依赖它的 SKU 不出现在集市列表；模块在线后恢复展示', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过模块 E2E 测试')

    const token = await loginUser(request)
    const adminToken = await loginAdmin(request)

    // 初始 firecrawl 离线，列表中不应包含 firecrawl SKU
    const offlineAgents = await listAgents(request, token)
    const offlineFirecrawl = offlineAgents.find((a) => a.id.startsWith('firecrawl-'))
    expect(offlineFirecrawl).toBeUndefined()

    // 管理员刷新 firecrawl 模块为在线
    const refreshResp = await request.post(`${apiBase}/v1/admin/modules/firecrawl/refresh`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(refreshResp.status()).toBe(200)

    // 列表中应出现 firecrawl 免费 SKU
    const onlineAgents = await listAgents(request, token)
    const onlineFirecrawl = onlineAgents.find((a) => a.id === 'firecrawl-scrape')
    expect(onlineFirecrawl).toBeDefined()

    // 恢复离线状态
    const delResp = await request.delete(`${apiBase}/v1/admin/modules/firecrawl`, {
      headers: { Authorization: `Bearer ${adminToken}` },
    })
    expect(delResp.status()).toBe(200)
    const reRegResp = await request.post(`${apiBase}/v1/admin/modules`, {
      headers: { Authorization: `Bearer ${adminToken}` },
      data: {
        module_id: 'firecrawl',
        name: 'Firecrawl',
        url: 'http://firecrawl:8080',
        transport_type: 'module',
        capabilities: ['scrape', 'crawl', 'extract'],
        version: '1.0.0',
      },
    })
    expect(reRegResp.status()).toBe(200)
  })
})

test.describe('SKU 凭证配置', () => {
  test('有 credentials 的 SKU 可打开配置弹窗并保存凭证', async ({ page, request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过凭证 E2E 测试')

    await loginAndSetStorage(page, request)
    await page.reload()

    // 找到 agent-reach-video 卡片，点击配置按钮
    const card = page.locator('.card', { hasText: '视频解析器' })
    await expect(card).toBeVisible()
    await card.locator('button[title="配置凭证"]').click()

    // 弹窗出现
    const modal = page.locator('text=视频解析器 凭证配置').locator('..').locator('..')
    await expect(page.locator('text=视频解析器 凭证配置')).toBeVisible()

    // 填写 YouTube Cookie
    const textarea = page.locator('textarea[placeholder="粘贴 YouTube Cookie"]')
    await textarea.fill('test_youtube_cookie_value')

    // 保存
    await page.locator('button', { hasText: '保存凭证' }).click()

    // 验证后端已保存
    const token = await loginUser(request)
    const credResp = await request.get(`${apiBase}/v1/agents/agent-reach-video/credentials`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(credResp.status()).toBe(200)
    const credData = await credResp.json()
    expect(credData.data.values.youtube_cookie).toBe('test_youtube_cookie_value')
  })
})
