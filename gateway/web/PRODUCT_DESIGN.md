# Eleball Web 产品设计文档

> 本文件面向 AI 编码助手与后续开发者，采用结构化、可检索的格式编写。  
> 范围：`gateway/web`（Eleball 用户端官网 + Web 对话）。  
> 目标：对齐 App 端核心能力，补齐 Web 端缺失功能，保证前后端接口契约一致。

---

## 1. 产品定位

Eleball Web 是 Eleball 手机端 AI 助手在桌面/移动浏览器中的延伸：

- **官网能力**：品牌展示、功能介绍、模型中心、下载引导。
- **在线对话**：与 App 共享同一套账户/余额/模型配置，支持 Ele Agent 代调用与 BYOK 自定义模型。
- **管理入口**：普通用户可配置自己的模型 Profile、查看余额、充值。

> 当前阶段：MVP 已跑通登录、模型切换、流式对话、新对话/历史、模型中心。  
> 下一阶段重点：补齐 App 端已有的富媒体对话渲染能力（Markdown、图片、图表、代码高亮等）。

---

## 2. 用户与场景

| 用户角色 | 核心场景 | Web 端诉求 |
|---------|---------|-----------|
| 普通用户 | 临时在 PC/浏览器上快速提问 | 打开即用、对话历史不丢、模型可切换 |
| 重度用户 | 长文本、代码、公式类问题 | Markdown 渲染、代码块、复制、图片/图表 |
| 开发者/BYOK | 使用自己的 API Key | 自定义 Base URL、模型名、API Key 本地存储 |
| 管理员 | 查看已配置模型 | 模型中心展示平台、模型名、单价 |

---

## 3. 信息架构与路由

```mermaid
graph LR
    A[/] -->|首页| B[Home]
    A -->|品牌/下载| B
    C[/chat] -->|在线对话| D[Chat]
    O[/video] -->|视频创作| P[VideoStudio]
    E[/models] -->|已配置模型列表| F[Models]
    G[/recharge] -->|余额充值| H[Recharge]
    M[/docs] -->|API 文档| N[Docs]
    I[/login 弹窗] -->|JWT 登录/注册| J[LoginModal]
    K[/settings 弹窗] -->|模型 Profile| L[ModelSettings]
```

| 路由 | 页面 | 访问控制 | 说明 |
|------|------|---------|------|
| `/` | Home | 公开 | 品牌 Hero、功能介绍、下载 App、对话演示动画 |
| `/chat` | Chat | 需登录 | 流式对话、对话历史、模型切换、新对话 |
| `/video` | VideoStudio | 需登录 | 文生视频、首帧图生视频、参数控制、历史任务管理 |
| `/models` | Models | 需登录 | 从网关拉取 Ele Agent 已配置模型，按平台筛选 |
| `/recharge` | Recharge | 需登录 | 弹丸/优雅币充值入口（当前占位） |
| `/docs` | Docs | 公开 | API 接入说明、接口概览与调用示例 |

---

## 4. 页面详细设计

### 4.1 Home（首页）

- **Hero 区**：紫色主题、新 Logo、简短 Slogan、CTA 到 `/chat` 或下载 App。
- **对话演示动画**：`ChatDemo.jsx` 模拟流式输出，展示产品形态。
- **功能区**：截图/OCR/选中文本、SubAgent、悬浮球特性简介。
- **模型中心入口**：链接到 `/models`。
- **Footer**：备案、隐私政策、服务条款、社交链接占位。

### 4.2 Chat（在线对话页）

#### 4.2.1 布局（三栏/单栏响应式）

```
┌────────────────────────────────────────────────────────────┐
│ Navbar (fixed top)                                          │
├──────────┬─────────────────────────────────────────────────┤
│ Sidebar  │ Header (模型选择 / 新对话 / 余额 / 设置)         │
│ 历史对话  ├─────────────────────────────────────────────────┤
│          │ Messages (可滚动)                                │
│          │                                                  │
│          ├─────────────────────────────────────────────────┤
│          │ Input (固定在底部)                               │
└──────────┴─────────────────────────────────────────────────┘
```

- **Sidebar**：对话历史列表、新对话、删除历史。
- **Header**：始终固定，包含：
  - 移动端 Sidebar 展开按钮
  - 品牌标识
  - 「新对话」按钮
  - 余额
  - 模型快速切换下拉框
  - 模型设置入口
- **Messages**：
  - 用户消息：右对齐、紫色气泡。
  - 助手消息：左对齐、灰色气泡、支持复制。
  - **待补齐**：Markdown 渲染、代码高亮、图片/图表、LaTeX。
- **Input**：固定在底部，多行文本框、发送按钮、当前模型提示。

#### 4.2.2 状态

| 状态 | 来源 | 持久化 |
|------|------|--------|
| 当前用户 / JWT | `AuthContext` | `localStorage` (`token`, `refresh_token`, `user`) |
| 模型 Profiles | `utils/model.js` | `localStorage` (`eleball_model_profiles`, `eleball_current_profile_id`) |
| 对话列表/当前对话 | `utils/conversation.js` | `localStorage` (`eleball_web_conversations`, `eleball_web_current_conversation_id`) |
| Ele Agent 模型选项 | 后端 `/eleagent/models` | 内存 |
| 余额 | 后端 `/billing/balance` | 内存 |
| 当前输入/加载态 | 组件 State | 不持久 |

#### 4.2.3 调用链（Ele Agent）

```
1. 用户发送消息
2. 如果是 Ele Agent 模式：
   a. 调用 GET /eleagent/credentials?subProvider=&subModel= 获取临时 baseUrl
   b. 用 JWT Token 请求 POST /chat/completions
   c. 后端用管理员配置的 Key 调用上游 SiliconFlow
3. 如果是 BYOK 模式：
   a. 直接使用用户填写的 Base URL + API Key
   b. 请求上游 OpenAI 兼容接口
4. 前端通过 SSE 解析流式内容
```

### 4.3 VideoStudio（视频创作页）

> v0.0.7 新增页面，提供独立的视频生成创作空间。

#### 4.3.1 布局

```
┌─────────────────────────────────────────────────────────────────┐
│ Navbar（固定顶部，含品牌、余额、模型切换、返回对话页）              │
├──────────────┬──────────────────────────────┬───────────────────┤
│              │                              │                   │
│ Sidebar      │     CreationPanel            │    PreviewPanel   │
│ 任务历史      │     - Prompt 输入框           │    - 视频播放器    │
│              │     - 首帧图上传              │    - 任务状态      │
│              │     - ratio / duration        │    - 参数摘要      │
│              │     - resolution / audio      │    - 下载 / 重试   │
│              │     - 生成按钮                │                   │
│              │                              │                   │
└──────────────┴──────────────────────────────┴───────────────────┘
```

#### 4.3.2 状态

| 状态 | 来源 | 持久化 |
|------|------|--------|
| 视频任务列表 | `useVideoTasks` | `localStorage` (`eleball_video_tasks_${user_id}`) |
| 当前选中任务 | 组件 State | 不持久 |
| 表单参数 | 组件 State | 不持久（P1 可保存为模板） |
| 模型选项 | 后端 `/eleagent/models` | 内存 |
| 余额 | 后端 `/billing/balance` | 内存 |

#### 4.3.3 调用链

```
1. 用户进入 /video 页面
2. 填写 Prompt、上传首帧图、设置参数
3. 点击「生成视频」
4. 前端 POST /v1/video/generations
5. 后端创建 Seedance 异步任务，返回 task_id
6. 前端将任务加入左侧列表并开始轮询
7. 任务成功后，右侧预览区展示视频播放器与下载按钮
```

#### 4.3.4 与对话页的关系

- `/chat` 作为发现入口：当用户选择 Seedance 模型时，输入框上方显示「去视频创作页生成视频」。
- `/video` 作为专业生产空间：承载参数控制、历史任务管理、结果预览。
- 未来支持将视频结果以卡片形式分享回 `/chat`（P2）。

### 4.4 Models（模型中心）

- 从 `/eleagent/models` 拉取网关已配置模型。
- 按 `provider` 分类筛选（Tab 形式）。
- 搜索模型名/显示名。
- 卡片展示：平台徽章、显示名、原始模型名、单价/免费。
- 未来可扩展：点击卡片直接跳转 `/chat` 并切换模型。

### 4.4 Recharge（充值）

- 当前为占位页。
- 未来支持微信支付、支付宝、优雅币套餐选择。

---

## 5. 组件清单

| 组件 | 路径 | 职责 |
|------|------|------|
| `Navbar` | `components/Navbar.jsx` | 顶部导航、登录状态、移动端菜单 |
| `Footer` | `components/Footer.jsx` | 页脚、备案信息 |
| `LoginModal` | `components/LoginModal.jsx` | 登录/注册弹窗 |
| `ModelSettings` | `components/ModelSettings.jsx` | 模型 Profile 增删改查、Provider 选择 |
| `ChatDemo` | `components/ChatDemo.jsx` | 首页对话演示动画 |
| `MessageBubble` | 内联于 `Chat.jsx` | 单条消息渲染、复制按钮 |
| `AuthContext` | `context/AuthContext.jsx` | JWT、登录态、自动刷新 |

### 5.1 视频创作页组件（v0.0.7 新增）

| 组件 | 路径 | 职责 |
|------|------|------|
| `VideoStudio` | `pages/VideoStudio.jsx` | 三栏布局、状态管理、任务列表 |
| `VideoTaskList` | `components/VideoTaskList.jsx` | 左侧历史任务列表 |
| `VideoTaskCard` | `components/VideoTaskCard.jsx` | 单个任务卡片 |
| `VideoCreationPanel` | `components/VideoCreationPanel.jsx` | 中间创作参数面板 |
| `VideoPreviewPanel` | `components/VideoPreviewPanel.jsx` | 右侧结果预览 |

### 5.2 待新增/拆分的组件

- [ ] `MarkdownRenderer`：统一渲染助手消息（Markdown、代码块、LaTeX、表格）。
- [ ] `CodeBlock`：代码高亮 + 一键复制。
- [ ] `ImageRenderer`：图片预览/放大。
- [ ] `ChartRenderer`：Mermaid / ECharts 图表渲染。
- [ ] `MessageToolbar`：复制、重新生成、点赞/点踩。
- [ ] `ThinkingBlock`：推理模型思考过程折叠展示。

---

## 6. 状态管理与工具函数

### 6.1 API 封装

`src/api/client.js` 基于 Axios：

- Base URL：`/api`（Vite dev proxy → `https://eleball.cn/v1`）
- 统一剥离 `{code, message, data}` 包装。
- 401 自动用 `refresh_token` 刷新。

### 6.2 SSE 解析

`src/utils/sse.js`：

- 原生 `fetch` 流式读取。
- 解析 `data: {...}`、`event: error`、`data: [DONE]`。
- 提取 `choices[0].delta.content`。

### 6.3 持久化工具

`src/utils/storage.js`：

- 所有 key 带 `eleball_` 前缀。
- `getJSON/setJSON` 安全读写 localStorage。

### 6.4 模型 Profile 工具

`src/utils/model.js`：

- `PROVIDERS`：Ele Agent / OpenAI / DeepSeek / Qwen / Moonshot / Custom。
- `resolveBaseUrl`：将后端返回的 `localhost` 替换为当前 `/api` 代理。
- `parseEleAgentModelName`：`provider/model_name` → `{subProvider, subModel}`。

### 6.5 对话记忆工具

`src/utils/conversation.js`：

- 基于 localStorage 的对话 CRUD。
- 自动生成对话标题。

---

## 7. 后端接口契约

所有接口路径前缀 `/v1`，成功响应统一包装：

```json
{
  "code": 0,
  "message": "success",
  "data": { ... }
}
```

### 7.1 Web 端当前使用接口

| 方法 | 路径 | 用途 | 字段 |
|------|------|------|------|
| POST | `/auth/login` | 登录 | `username`, `password`, `device_id` |
| POST | `/auth/register` | 注册 | `username`, `password`, `device_id` |
| POST | `/auth/refresh` | 刷新 Token | `refresh_token` |
| GET  | `/auth/me` | 当前用户 | - |
| GET  | `/billing/balance` | 余额 | 返回 `danwan`, `elegant`, `unit` |
| GET  | `/eleagent/models` | 已配置模型 | 返回数组 `{provider, model_name, display_name, price_per_call, priority}` |
| GET  | `/eleagent/credentials` | Ele Agent 临时凭证 | `subProvider`, `subModel` |
| POST | `/chat/completions` | 流式对话 | `provider`, `model`, `messages`, `stream` |
| POST | `/video/generations` | 创建视频生成任务 | `provider`, `model`, `prompt`, `first_frame`, `params` |
| GET  | `/video/generations/:id` | 查询视频生成任务 | - |

### 7.2 字段命名

- 统一使用 **snake_case**：`access_token`, `refresh_token`, `user_id`, `device_id`, `model_name`, `base_url`, `api_key`, `price_per_call`。
- 前端组件内部使用 camelCase，边界处转换由 API 拦截器/工具函数处理。

---

## 8. Web vs App 功能对齐矩阵

| 功能模块 | App 端 | Web 端当前 | 优先级 |
|---------|--------|-----------|--------|
| 登录/注册/Token 刷新 | ✅ | ✅ | P0 |
| Ele Agent 代调用 | ✅ | ✅ | P0 |
| BYOK 自定义模型 | ✅ | ✅ | P0 |
| 流式对话 | ✅ | ✅ | P0 |
| 模型切换 / Profile 管理 | ✅ | ✅ | P0 |
| 对话历史 / 新对话 | ✅ | ✅ | P0 |
| 余额展示 | ✅ | ✅ | P0 |
| 模型中心 | ✅ | ✅ | P0 |
| **Markdown 渲染** | ✅ | ❌ 纯文本 | **P1** |
| **代码高亮 + 复制** | ✅ | ❌ 仅整段复制 | **P1** |
| **图片/图表渲染** | ✅ | ❌ 不渲染 | **P1** |
| LaTeX 公式 | ✅ | ❌ | P2 |
| 消息重新生成/点赞点踩 | ✅ | ❌ | P2 |
| 语音输入/朗读 | ✅ | ❌ | P3 |
| 截图/OCR/选中文本 | ✅ | ❌（浏览器能力受限） | P3 |
| 悬浮球触发 | ✅ | ❌ 不适用 | - |
| **视频生成创作页** | ✅（阶段二） | ❌ | **P0** |
| 充值/支付 | 部分 | 占位 | P2 |

---

## 9. 缺失功能实现路线图

### P1：富文本消息渲染

#### 9.1 Markdown → HTML

- 引入 `react-markdown` + `remark-gfm` + `remark-math` + `rehype-katex`。
- 助手消息统一走 `MarkdownRenderer`。
- 保留用户消息为纯文本（用户一般只输入文本）。

#### 9.2 代码块

- 自定义 `CodeBlock` 组件：
  - 语法高亮：`prismjs` 或 `react-syntax-highlighter`。
  - 语言标签、复制按钮。
  - 行号（可选）。

#### 9.3 图片

- 检测助手消息中的 `![alt](url)` 或 HTML `<img>`。
- 使用懒加载 + 点击放大（Lightbox）。
- 若模型返回的是 Markdown 图片语法，`react-markdown` 默认可渲染。

#### 9.4 图表

- **Mermaid**：检测 ````mermaid` 代码块，用 `mermaid` 库渲染为 SVG。
- **ECharts**：若模型输出 JSON chart config，用 `echarts-for-react` 渲染。
- 安全策略：图表渲染在沙箱 iframe 或严格白名单配置内进行，避免 XSS。

#### 9.5 表格/列表/链接

- `remark-gfm` 支持表格、任务列表、删除线。
- 链接默认新窗口打开，并增加 `rel="noopener noreferrer"`。

### P2：交互增强

- 单条消息重新生成（`regenerate`）。
- 点赞/点踩反馈。
- 思考过程折叠（Reasoning 模型）。

### P3：浏览器受限能力

- 截图/OCR：浏览器端可通过 Clipboard API 读取图片、或用户主动上传。
- 语音输入：Web Speech API。
- 悬浮球：Web 端不适用，保留为浏览器标签页/快捷方式。

### P4：视频生成（v0.0.7）

- 接入火山引擎 Seedance 视频大模型，支持文生视频与首帧图生视频。
- 新增独立 `/video` 视频创作页，支持参数控制与历史任务管理。
- 后端 Gateway 新增 `/v1/video/generations` 异步任务接口。
- 管理员后台配置 Seedance 模型、API Key、单价。
- 按视频生成 token 用量扣减弹丸/优雅弹丸。
- 预留 LibTV Skill 接入扩展点。

### P5：AI Agent 工具流

- 在 `/chat` 对话页内自动触发工具调用（搜索、读文件、OCR、视频生成等）。
- VIP 用户可调用服务器端工具链（2–3 步），结果按 `session_id` 归档。
- 非 VIP 用户上传需工具处理的文件时返回升级提示。
- 工具调用不额外计费，仅按目标模型 token 计费。
- UI 上以统一进度状态条展示工具执行过程。

---

## 10. 设计系统

### 10.1 色彩（与 App 对齐）

```css
--eleball-bg: #FFFFFF;
--eleball-surface: #FFFFFF;
--eleball-surface-variant: #F3F0F7;
--eleball-primary: #6750A4;
--eleball-primary-light: #EADDFF;
--eleball-primary-dark: #21005D;
--eleball-secondary: #625B71;
--eleball-text: #1C1B1F;
--eleball-text-secondary: #49454F;
--eleball-text-tertiary: #79747E;
--eleball-outline: #E2E0E5;
--eleball-outline-variant: #F0EDF4;
--eleball-error: #B3261E;
--eleball-success: #22C55E;
```

### 10.2 字体

- 系统字体栈：`-apple-system, BlinkMacSystemFont, Segoe UI, PingFang SC, Noto Sans SC, Microsoft YaHei, sans-serif`。
- Markdown 代码块使用等宽字体：`ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace`。

### 10.3 圆角与阴影

- 按钮/卡片：`rounded-3xl` 或 `rounded-2xl`。
- 气泡：`rounded-2xl`，用户气泡 `rounded-br-md`，助手气泡 `rounded-bl-md`。
- 主按钮阴影：`shadow-md`。

---

## 11. 开发/构建/部署

### 11.1 本地开发

```bash
cd gateway/web
npm install
npm run dev      # http://localhost:5174
```

Vite 代理：`/api` → `https://eleball.cn/v1`（域名备案后的真实后端）。

### 11.2 生产构建

```bash
npm run build
# 产物在 gateway/web/dist/
```

### 11.3 Docker 部署

- 已接入 `gateway/deployments/docker-compose.yml`。
- Web 容器端口 `8082`，通过 Nginx 统一入口 `80`。
- 生产环境需设置 `VITE_API_BASE` 为真实网关域名 `/v1` 或绝对 URL。

---

## 12. AI 编码规范

- 所有新增页面/组件放在 `src/pages/` 或 `src/components/`。
- 工具函数优先放在 `src/utils/`，按职责拆分文件。
- API 调用统一走 `src/api/client.js` 暴露的函数，不要直接写 Axios。
- 敏感信息（API Key）只存 localStorage，不上传服务器（Ele Agent 除外）。
- 状态持久化使用 `src/utils/storage.js` 前缀 `eleball_`。
- 新增 Schema/接口必须先改 `specs/api-schema.yml`，再改前后端代码。
- Markdown/图表渲染必须做 XSS 防护，禁止直接 `dangerouslySetInnerHTML`。

---

## 13. 近期待办（按优先级排序）

1. [ ] 接入 `react-markdown` 渲染助手消息。
2. [ ] 代码块高亮 + 复制按钮。
3. [ ] Mermaid 图表渲染。
4. [ ] 图片懒加载与点击放大。
5. [ ] 单条消息「重新生成」。
6. [ ] 充值页接入真实支付接口。
7. [ ] 后端 `/models` 公共接口（如需展示 BYOK 供应商静态模型）。
8. [ ] 对话历史后端同步（`/v1/sync/pull`, `/v1/sync/push`）。
9. [ ] 接入 Seedance 视频生成（v0.0.7）：新增 `/video` 独立创作页，支持文生视频、首帧图生视频、参数控制、历史任务管理。
10. [ ] `/chat` 页增加「去视频创作页」快捷入口（P1）。
11. [ ] 预留 LibTV Skill 视频工作流接入（v0.0.8+）。

---

## 14. 参考文件

| 文件 | 说明 |
|------|------|
| `gateway/web/src/pages/Chat.jsx` | 对话页主实现 |
| `gateway/web/src/pages/Models.jsx` | 模型中心 |
| `gateway/web/src/components/ModelSettings.jsx` | 模型 Profile 配置 |
| `gateway/web/src/utils/sse.js` | SSE 流式解析 |
| `gateway/web/src/utils/model.js` | 模型 Profile 工具 |
| `gateway/web/src/utils/conversation.js` | 对话本地存储 |
| `specs/api-schema.yml` | 后端接口正式契约 |
| `docs/04-ux-spec.md` | App 端交互规范 |
