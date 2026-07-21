import { defineConfig, devices } from '@playwright/test'

/**
 * Eleball Web E2E 测试配置
 * 默认读取环境变量：
 *   E2E_BASE_URL=http://localhost:5174
 *   E2E_API_URL=http://localhost:8080
 */
export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://localhost:5174',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'npm run dev -- --port 5174 --host',
    url: 'http://localhost:5174',
    reuseExistingServer: !process.env.CI,
    timeout: 120 * 1000,
    env: {
      E2E_API_URL: process.env.E2E_API_URL || 'http://localhost:8080',
      // E2E 环境放开支付入口（与 start-local.ps1 一致），生产构建默认禁用
      VITE_PAYMENT_ENABLED: 'true',
    },
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
