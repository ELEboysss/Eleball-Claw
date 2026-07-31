import { test, expect } from '@playwright/test'

/**
 * Agent Team 组管理 E2E 测试（P4）
 * 默认读取环境变量：
 *   E2E_BASE_URL=http://localhost:5174
 *   E2E_API_URL=http://localhost:8080
 * 运行前请确保后端 Gateway（或 e2e-server）已启动，否则用例自动跳过。
 *
 * 覆盖：
 *  - 组 CRUD + 组共享记忆闭环（API 契约，request 直连后端）
 *  - 对话归组：PATCH team_id + 按组过滤（API 契约）
 *  - 对话侧栏「分组管理」弹窗可打开（UI smoke）
 *
 * 注：CallAssistant 委派展示用例由真实 LLM 驱动，e2e-server 不模拟，单独跟进。
 */

const apiBase = process.env.E2E_API_URL || 'http://localhost:8080'
const testUser = {
  username: 'e2e_team@eleball.app',
  password: 'e2e_password',
  device_id: 'e2e-team-device',
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

async function loginAndGetToken(request, user = testUser) {
  await ensureTestAccount(request)
  const resp = await request.post(`${apiBase}/v1/auth/login`, { data: user })
  if (resp.status() !== 200) {
    throw new Error('登录失败，请确认后端已启动且测试账号可用')
  }
  const { data } = await resp.json()
  return data.access_token
}

async function loginAndSetStorage(page, request, user = testUser) {
  const token = await loginAndGetToken(request, user)
  const meResp = await request.get(`${apiBase}/v1/auth/me`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  const me =
    meResp.status() === 200
      ? (await meResp.json()).data
      : { user_id: 'e2e-team', username: user.username, role: 'user' }
  await page.goto('/chat')
  await page.evaluate(({ token, user }) => {
    localStorage.setItem('eleball_token', token)
    localStorage.setItem('eleball_user', JSON.stringify(user))
  }, { token, user: me })
}

test.describe('Agent Team 组管理 API', () => {
  test('teams CRUD + 组记忆闭环', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过组管理 API E2E 测试')
    const token = await loginAndGetToken(request)
    const h = { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }

    // 创建组
    const createResp = await request.post(`${apiBase}/v1/teams`, {
      headers: h,
      data: { name: 'E2E 组', description: '自动化测试用' },
    })
    expect(createResp.status()).toBe(200)
    const team = (await createResp.json()).data
    expect(team.id).toBeTruthy()

    // 列表含该组
    const listResp = await request.get(`${apiBase}/v1/teams`, { headers: h })
    expect(listResp.status()).toBe(200)
    const list = (await listResp.json()).data
    expect(Array.isArray(list)).toBeTruthy()
    expect(list.some((t) => t.id === team.id)).toBeTruthy()

    // 详情
    const getResp = await request.get(`${apiBase}/v1/teams/${team.id}`, { headers: h })
    expect(getResp.status()).toBe(200)
    const detail = (await getResp.json()).data
    expect(detail.name).toBe('E2E 组')
    expect(Array.isArray(detail.conversations)).toBeTruthy()

    // 组记忆：新增 -> 列表 -> 删除
    const memResp = await request.post(`${apiBase}/v1/teams/${team.id}/memories`, {
      headers: h,
      data: { content: 'E2E 沉淀的事实', tags: '测试,e2e' },
    })
    expect(memResp.status()).toBe(200)
    const mem = (await memResp.json()).data
    expect(mem.id).toBeTruthy()

    const memListResp = await request.get(`${apiBase}/v1/teams/${team.id}/memories`, { headers: h })
    expect(memListResp.status()).toBe(200)
    const memList = (await memListResp.json()).data
    expect(memList.items.some((m) => m.id === mem.id)).toBeTruthy()

    const delMemResp = await request.delete(`${apiBase}/v1/teams/${team.id}/memories/${mem.id}`, { headers: h })
    expect(delMemResp.status()).toBe(200)

    // 改名
    const updResp = await request.patch(`${apiBase}/v1/teams/${team.id}`, {
      headers: h,
      data: { name: 'E2E 组-改名' },
    })
    expect(updResp.status()).toBe(200)
    expect((await updResp.json()).data.name).toBe('E2E 组-改名')

    // 删除组
    const delResp = await request.delete(`${apiBase}/v1/teams/${team.id}`, { headers: h })
    expect(delResp.status()).toBe(200)
  })

  test('PATCH /v1/conversations/:id 可移动归组并按组过滤', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过归组 E2E 测试')
    const token = await loginAndGetToken(request)
    const h = { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` }

    // 创建组 + 对话
    const team = (await (await request.post(`${apiBase}/v1/teams`, { headers: h, data: { name: '归组测试' } })).json()).data
    const conv = (await (await request.post(`${apiBase}/v1/conversations`, { headers: h, data: { title: 'E2E 归组对话' } })).json()).data

    // 移入组
    const moveResp = await request.patch(`${apiBase}/v1/conversations/${conv.id}`, {
      headers: h,
      data: { team_id: team.id },
    })
    expect(moveResp.status()).toBe(200)

    // 按组过滤应包含该对话
    const filtered = await request.get(`${apiBase}/v1/conversations?team_id=${team.id}`, { headers: h })
    expect(filtered.status()).toBe(200)
    const filteredData = (await filtered.json()).data
    const items = filteredData.items || filteredData
    expect(items.some((c) => c.id === conv.id)).toBeTruthy()

    // 移出组（team_id 置空）
    const outResp = await request.patch(`${apiBase}/v1/conversations/${conv.id}`, {
      headers: h,
      data: { team_id: '' },
    })
    expect(outResp.status()).toBe(200)

    // 清理
    await request.delete(`${apiBase}/v1/teams/${team.id}`, { headers: h })
  })
})

test.describe('Agent Team 组管理 UI', () => {
  test('对话侧栏可打开分组管理弹窗', async ({ page, request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过组管理 UI 测试')
    await loginAndSetStorage(page, request)
    await page.reload()
    const btn = page.getByRole('button', { name: '分组管理' })
    await btn.waitFor({ timeout: 10000 })
    await btn.click()
    // 弹窗标题与「新建分组」按钮可见（弹窗为客户端渲染，组列表请求失败时显示空态）
    await expect(page.getByRole('heading', { name: '分组管理' })).toBeVisible()
    await expect(page.getByRole('button', { name: '新建分组' })).toBeVisible()
  })
})
