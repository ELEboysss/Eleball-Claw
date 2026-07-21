# Eleball Web — 用户端官网与 H5 对话服务

Eleball 官方用户端 Web 入口，包含 OpenAI 风格的官网首页、H5 在线对话页、Token 充值页。

## 技术栈

- React 18 + Vite 5
- react-router-dom 6
- Tailwind CSS 3
- Axios
- Framer Motion
- lucide-react

## 开发环境

```bash
cd gateway/web
npm install
npm run dev
```

开发服务器默认运行在 `http://localhost:5174`。

## 环境变量

复制 `.env.template` 为 `.env`：

```bash
cp .env.template .env
```

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `VITE_API_BASE` | API 基础路径 | `/api` |

开发时 Vite 会将 `/api` 代理到 `http://localhost:8080/v1`。

## 生产构建

```bash
npm run build
```

构建产物输出到 `dist/` 目录。

## E2E 测试

基于 Playwright 的浏览器自动化测试，位于 `e2e/` 目录。

```bash
# 首次运行需安装浏览器二进制文件
npm run e2e:install

# 运行全部 E2E 测试（默认自动启动前端 preview 服务）
npm run e2e

# 交互式调试模式
npm run e2e:ui
```

环境变量：

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `E2E_BASE_URL` | 前端基础地址 | `http://localhost:5174` |
| `E2E_API_URL` | 后端 Gateway 地址 | `http://localhost:8080` |

> 若后端未启动，涉及后端接口的测试会自动跳过；未登录 UI 测试不依赖后端。

## 页面说明

| 路由 | 页面 | 功能 |
|------|------|------|
| `/` | 官网首页 | 品牌展示、核心能力、模型矩阵、定价入口 |
| `/chat` | H5 对话页 | 登录、模型选择、SSE 流式对话、余额提示 |
| `/recharge` | 充值页 | 余额展示、Token 套餐、兑换码充值 |
| `/models` | 模型中心 | 展示 Ele Agent 支持的模型列表，按平台筛选 |
| `/docs` | 文档页 | API 接入说明、接口概览与调用示例 |

## 后端接口

对接 `gateway/` 的以下接口：

- `POST /v1/auth/login`
- `POST /v1/auth/register`
- `POST /v1/auth/refresh`
- `GET /v1/auth/me`
- `GET /v1/eleagent/models`
- `POST /v1/chat/completions`（SSE 流式）
- `GET /v1/billing/balance`
- `POST /v1/payment/wechat/prepay`
- `POST /v1/payment/alipay/order`

## 设计系统

Logo 使用新版紫色水晶弹珠滑稽表情图标，源文件位于 `docs/assets/eleball-icon.png`。

Web 端配色对齐 App 端 `ChatBubbleWindow` 使用的 Material 3 默认色板，并进一步简化为纯白底：

- 背景：#FFFFFF
- 主强调色：#6750A4
- 主强调浅色：#EADDFF
- 文字主色：#1C1B1F

## 注意事项

- localStorage key 使用 `eleball_` 前缀，避免与 `gateway/admin-web/` 的 `admin_` 前缀冲突。两者共用同一域名时，分别部署在 `/` 与 `/admin/`。
- 登录/注册字段使用 `username` / `password` / `device_id`（snake_case），与当前后端实现保持一致。
- 支付接口 MVP 阶段为骨架实现，真实支付能力需后续补齐。
