import { test, expect } from '@playwright/test'

/**
 * Chat 页面基础 E2E 测试
 * 默认读取环境变量：
 *   E2E_BASE_URL=http://localhost:5174
 *   E2E_API_URL=http://localhost:8080
 * 运行前请确保后端 Gateway 已启动，否则需要后端交互的测试会自动跳过。
 */

const apiBase = process.env.E2E_API_URL || 'http://localhost:8080'
const testUser = {
  username: 'e2e_user@eleball.app',
  password: 'e2e_password',
  device_id: 'e2e-device',
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

  // 同步前端 AuthContext 所需的 token 与 user
  const meResp = await request.get(`${apiBase}/v1/auth/me`, {
    headers: { Authorization: `Bearer ${data.access_token}` },
  })
  const me = meResp.status() === 200 ? (await meResp.json()).data : { user_id: data.user_id, username: user.username, role: 'user' }

  await page.goto('/chat')
  await page.evaluate(({ token, user }) => {
    localStorage.setItem('eleball_token', token)
    localStorage.setItem('eleball_user', JSON.stringify(user))
  }, { token: data.access_token, user: me })
}

test.describe('Chat 页面', () => {
  test('未登录状态显示登录引导', async ({ page }) => {
    await page.goto('/chat')
    await expect(page.getByText('登录后体验完整对话')).toBeVisible()
    await expect(page.locator('main').getByRole('button', { name: /登录/ })).toBeVisible()
  })

  test('登录后页面包含 Agent 工具开关与消息输入框', async ({ page, request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过登录态 UI 测试')

    await loginAndSetStorage(page, request)
    await page.reload()
    const agentButton = page.getByRole('button', { name: 'Agent', exact: true })
    await agentButton.waitFor({ timeout: 10000 })
    await expect(agentButton).toBeVisible()
    await expect(page.getByPlaceholder('输入消息，或粘贴/上传图片、文件…')).toBeVisible()
    await expect(page.getByRole('button', { name: '发送消息' })).toBeVisible()
  })
})

test.describe('Agent 执行 API', () => {
  test('POST /v1/agent/execute 返回 SSE 事件流', async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过 Agent API E2E 测试')

    await ensureTestAccount(request)
    const loginResp = await request.post(`${apiBase}/v1/auth/login`, { data: testUser })
    expect(loginResp.status()).toBe(200)
    const { data } = await loginResp.json()
    const token = data?.access_token

    // 使用普通对话（enable_tools=false）验证 SSE 响应可正常结束
    const resp = await request.fetch(`${apiBase}/v1/agent/execute`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      data: {
        conversation_id: 'e2e_conv_agent',
        message: '你好',
        enable_tools: false,
      },
      timeout: 30000,
    })
    expect(resp.status()).toBe(200)
    expect(resp.headers()['content-type']).toContain('text/event-stream')
    const body = await resp.text()
    expect(body).toContain('event:')
  })

  test('VIP 用户启用 Agent 工具与联网搜索后 API 可触发工具调用', async ({ page, request }) => {
    test.skip(!(await isBackendReachable(request)), '后端服务未启动，跳过 Agent 工具 E2E 测试')

    // 使用 admin 账号登录（VIP 才能使用 ServerSide 工具）
    const adminUser = { username: 'admin', password: 'admin123', device_id: 'e2e-admin-device' }
    await loginAndSetStorage(page, request, adminUser)
    await page.reload()
    const agentButton = page.getByRole('button', { name: 'Agent', exact: true })
    await agentButton.waitFor({ timeout: 10000 })

    // 验证可点击 Agent 开关按钮
    await expect(agentButton).toBeVisible()
    await agentButton.click()
    // 点击后按钮应变为激活态（蓝色样式）
    await expect(agentButton).toHaveClass(/bg-blue-50/)

    // 通过直接调用 SSE API 验证事件流包含 SearchWeb 工具调用
    const token = await page.evaluate(() => localStorage.getItem('eleball_token'))
    const resp = await request.fetch(`${apiBase}/v1/agent/execute`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`,
      },
      data: {
        message: '帮我查一下今天的新闻',
        enable_tools: true,
        enable_web_search: true,
        search_provider: 'baidu',
        model: process.env.E2E_AGENT_MODEL || 'kimi-latest',
      },
      timeout: 60000,
    })
    expect(resp.status()).toBe(200)
    expect(resp.headers()['content-type']).toContain('text/event-stream')
    const body = await resp.text()
    expect(body).toContain('event: tool_call')
    expect(body).toContain('SearchWeb')
    expect(body).toContain('event: tool_result')
  })
})
