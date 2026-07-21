import { test, expect } from '@playwright/test'

/**
 * 支付宝收银台 E2E 测试（单次支付链路）
 *
 * 前置：E2E 后端（gateway/cmd/e2e-server）已启动。
 * 默认读取环境变量：
 *   E2E_BASE_URL=http://localhost:5174
 *   E2E_API_URL=http://localhost:8080
 *
 * 覆盖链路：扫码下单（precreate）→ 收银台二维码 → mock 支付宝回调（notify）→
 * 轮询确认支付成功 → 权益到账（弹丸余额 / VIP 开通）。
 */

const apiBase = process.env.E2E_API_URL || 'http://localhost:8080'
const testUser = {
  username: 'e2e_ui_pay_user@eleball.app',
  password: 'e2e_password',
  device_id: 'e2e-ui-pay-device',
}
const adminUser = { username: 'admin', password: 'admin123', device_id: 'e2e-ui-pay-admin' }

// 本次测试创建的套餐/VIP 参数（danwan 与价格分故意取同值，便于断言）
// fullyParallel 下每个 worker 各跑一次 beforeAll，名称带唯一后缀避免重复卡片冲突
const SUFFIX = Date.now().toString(36).slice(-6)
const PKG = { name: `E2E支付测试包${SUFFIX}`, danwan: 321, price_yuan: 3.21, sort_order: 1, is_enabled: true }
const PKG_AMOUNT_FEN = Math.round(PKG.price_yuan * 100)
const VIP_PLAN = {
  level: 5, name: `E2E支付VIP${SUFFIX}`, price_yuan: 9.9, duration_days: 30,
  discount_percent: 100, max_conversations: 0, max_agent_sessions: 0,
  asr_quota_monthly: 0, agent_enabled: true, file_tools_enabled: false,
  sort_order: 1, is_enabled: true, description: 'E2E 支付链路测试套餐',
}

async function isBackendReachable(request) {
  try {
    const ping = await request.get(`${apiBase}/health`, { timeout: 3000 })
    return ping.status() === 200
  } catch (e) {
    return false
  }
}

async function login(request, user) {
  await request.post(`${apiBase}/v1/auth/register`, { data: user }).catch(() => {})
  const resp = await request.post(`${apiBase}/v1/auth/login`, { data: user })
  expect(resp.status(), `登录失败: ${user.username}`).toBe(200)
  const { data } = await resp.json()
  return data.access_token
}

async function loginAndSetStorage(page, request) {
  const token = await login(request, testUser)
  const meResp = await request.get(`${apiBase}/v1/auth/me`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  const me = meResp.status() === 200 ? (await meResp.json()).data : { user_id: '', username: testUser.username, role: 'user' }

  await page.goto('/recharge')
  await page.evaluate(({ token, user }) => {
    localStorage.setItem('eleball_token', token)
    localStorage.setItem('eleball_user', JSON.stringify(user))
  }, { token, user: me })
  await page.reload()
  return token
}

async function getBalance(request, token) {
  const resp = await request.get(`${apiBase}/v1/billing/balance`, {
    headers: { Authorization: `Bearer ${token}` },
  })
  const { data } = await resp.json()
  return data.danwan
}

// 点击按钮并捕获 precreate 响应中的订单信息
async function clickAndCapturePrecreate(page, button) {
  const [resp] = await Promise.all([
    page.waitForResponse(
      (r) => r.url().includes('/api/payment/alipay/precreate') && r.request().method() === 'POST',
      { timeout: 10000 },
    ),
    button.click(),
  ])
  const body = await resp.json()
  expect(body.code, `precreate 失败: ${body.message}`).toBe(0)
  return body.data // { order_id, qr_code, amount_fen, status }
}

// 模拟支付宝异步通知（E2E 服务器免验签 mock）
async function mockAlipayNotify(request, orderId, amountYuan) {
  const resp = await request.post(`${apiBase}/v1/payment/alipay/notify`, {
    form: {
      out_trade_no: orderId,
      trade_status: 'TRADE_SUCCESS',
      trade_no: `E2E_MOCK_${Date.now()}`,
      total_amount: amountYuan,
      app_id: 'e2e',
    },
  })
  expect(await resp.text()).toBe('success')
}

test.describe('支付宝收银台', () => {
  test.beforeAll(async ({ request }) => {
    test.skip(!(await isBackendReachable(request)), 'E2E 后端未启动，跳过支付 E2E 测试')
    const adminToken = await login(request, adminUser)
    const headers = { Authorization: `Bearer ${adminToken}` }

    // 准备充值套餐与 VIP 套餐（重复创建无害，E2E 数据每次运行独立）
    const pkgResp = await request.post(`${apiBase}/v1/admin/recharge/packages`, { headers, data: PKG })
    expect(pkgResp.status()).toBe(200)
    const planResp = await request.post(`${apiBase}/v1/admin/vip/plans`, { headers, data: VIP_PLAN })
    expect(planResp.status()).toBe(200)
  })

  test('弹丸充值：支付宝扫码下单 → mock 回调 → 支付成功 → 余额到账', async ({ page, request }) => {
    const token = await loginAndSetStorage(page, request)
    const balanceBefore = await getBalance(request, token)

    // 选择测试套餐并发起支付宝支付
    await page.getByRole('heading', { name: PKG.name, exact: true }).first().click()
    const order = await clickAndCapturePrecreate(
      page,
      page.getByRole('button', { name: '支付宝支付' }),
    )
    expect(order.amount_fen).toBe(PKG_AMOUNT_FEN)
    expect(order.qr_code).toBeTruthy()

    // 收银台模态：二维码可见
    await expect(page.getByText('支付宝扫码支付')).toBeVisible()
    await expect(page.getByAltText('支付宝支付二维码')).toBeVisible()

    // 模拟支付宝回调 → 收银台轮询确认成功
    await mockAlipayNotify(request, order.order_id, PKG.price_yuan.toFixed(2))
    await expect(page.getByText('支付成功')).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: '完成' }).click()

    // 余额到账
    const balanceAfter = await getBalance(request, token)
    expect(balanceAfter - balanceBefore).toBe(PKG.danwan)
  })

  test('VIP 开通：支付宝收银台 → mock 回调 → VIP 生效', async ({ page, request }) => {
    const token = await loginAndSetStorage(page, request)

    // 选择测试 VIP 套餐并发起开通
    await page.getByRole('heading', { name: VIP_PLAN.name, exact: true }).first().click()
    const order = await clickAndCapturePrecreate(
      page,
      page.getByRole('button', { name: /支付宝开通/ }),
    )

    // 收银台 → mock 回调 → 支付成功
    await expect(page.getByText('支付宝扫码支付')).toBeVisible()
    await mockAlipayNotify(request, order.order_id, VIP_PLAN.price_yuan.toFixed(2))
    await expect(page.getByText('支付成功')).toBeVisible({ timeout: 10000 })
    await page.getByRole('button', { name: '完成' }).click()

    // 页面 VIP 状态刷新为已开通
    await expect(page.getByText(`当前 VIP${VIP_PLAN.level}`)).toBeVisible({ timeout: 10000 })

    // API 侧确认 VIP 生效
    const statusResp = await request.get(`${apiBase}/v1/vip/status`, {
      headers: { Authorization: `Bearer ${token}` },
    })
    const { data } = await statusResp.json()
    expect(data.is_vip).toBe(true)
    expect(data.level).toBe(VIP_PLAN.level)
  })
})
