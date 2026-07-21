# Eleball Gateway — 后端网关

> Eleball 可选后端网关，提供代调用、多设备同步、Agent 市场、计费与管理后台能力。

## 技术栈

| 层级 | 选型 | 版本 |
|------|------|------|
| 语言 | Go | 1.25+ |
| Web 框架 | Gin | v1.12+ |
| ORM | GORM | v1.31+ |
| 数据库 | SQLite（开发）/ PostgreSQL（生产） | — |
| 日志 | Zap | v1.28+ |
| 配置 | Viper | v1.21+ |
| JWT | golang-jwt/jwt | v5.3+ |

## 目录结构

```
gateway/
├── cmd/server/         # 入口 main.go
├── internal/
│   ├── config/         # Viper 配置加载
│   ├── handler/        # HTTP Handler（认证、聊天、计费、同步、支付、Agent、提现、管理后台）
│   ├── service/        # 业务逻辑层
│   ├── model/          # GORM 数据模型
│   ├── repository/     # 数据访问层
│   ├── middleware/     # JWT、限流、CORS、日志
│   └── router/         # Gin 路由注册
├── pkg/
│   ├── llm/            # OpenAI / DeepSeek 流式代理客户端
│   └── util/           # JWT、Hash 工具
├── configs/
│   └── config.yaml     # 默认配置文件
├── deployments/
│   ├── Dockerfile
│   └── docker-compose.yml
├── admin-web/          # 管理后台前端（React + Vite）
└── Makefile
```

## 快速开始

### 1. 配置

默认配置文件位于 `configs/config.yaml`：

```yaml
server:
  port: 8080
  mode: release        # release / debug / test

database:
  driver: sqlite
  dsn: "eleball.db"    # SQLite 文件路径（相对或绝对）

jwt:
  secret: "change-me-in-production"
  access_expire_hours: 2
  refresh_expire_hours: 720

llm:
  timeout: 60s
  max_retries: 3

rate_limit:
  requests_per_minute: 180
  read_multiplier: 3.0
```

**环境变量覆盖**：
- `CONFIG_PATH`：自定义配置文件路径
- `OPENAI_API_KEY`：注册 OpenAI 代理客户端
- `DEEPSEEK_API_KEY`：注册 DeepSeek 代理客户端
- `AGENT_API_KEY` / `AGENT_BASE_URL`：Agent 工作流兜底 LLM 客户端配置（可选）。Web 端仅对 Ele Agent 模型展示 Agent 工具开关；调用 `/v1/agent/execute` 时传入用户当前 Ele Agent 模型配置，Agent 工作流根据该配置选择目标请求方。仅当请求未传入 provider 时才回退到该配置。
- `BAIDU_API_KEY`：百度千帆 AppBuilder API Key（百度 AI 搜索 API，每日 100 次免费额度，国内首选默认源）。配置后前端展示“百度”搜索选项
- `BING_SEARCH_API_KEY`：Bing Web Search API Key（付费稳定，适合 VIP/企业用户）。配置后前端展示“Bing”搜索选项
- `BAIDU_SEARCH_ENDPOINT`：百度 AI 搜索 API 地址（可选，默认 `https://qianfan.baidubce.com/v2/ai_search/web_search`）
- `SEARXNG_URL`：自建 SearXNG 地址（后端保留兼容，前端暂不展示）
- `QWEN_API_KEY` / `SILICONFLOW_API_KEY`：Ele Agent 默认上游模型 API Key

### 2. 编译

#### 方式 A：纯 Go 编译（推荐，无需 CGO）

适用于 Windows 开发环境或交叉编译：

```bash
cd gateway
make build-nocgo
# 或手动：CGO_ENABLED=0 go build -o build/eleball-gateway ./cmd/server
```

> 当前代码已切换至 `modernc.org/sqlite` 纯 Go SQLite 驱动，无需安装 gcc/mingw。

#### 方式 B：传统 CGO 编译（需 gcc）

```bash
cd gateway
make build
```

### 3. 运行

```bash
cd gateway
make run
# 或
./build/eleball-gateway
```

服务启动后：
- API 地址：`http://localhost:8080`
- 健康检查：`GET http://localhost:8080/health`

### 4. Docker 部署（生产推荐）

```bash
cd gateway
docker-compose up --build
```

如需切换 PostgreSQL，请取消注释 `deployments/docker-compose.yml` 中的 postgres 服务，并修改 `configs/config.yaml` 的 `database` 段。

## API 概览

| 方法 | 路径 | 说明 | 认证 |
|------|------|------|------|
| POST | /v1/auth/register | 用户注册 | 公开 |
| POST | /v1/auth/login | 用户登录 | 公开 |
| POST | /v1/auth/refresh | 刷新 Token | 公开 |
| POST | /v1/chat/completions | 流式对话代理 | JWT |
| GET | /v1/billing/balance | 查询余额 | JWT |
| POST | /v1/sync/push | 推送同步数据 | JWT |
| POST | /v1/sync/pull | 拉取同步数据 | JWT |
| POST | /v1/payment/wechat/prepay | 微信支付预下单 | JWT |
| POST | /v1/payment/alipay/order | 支付宝下单 | JWT |
| GET | /v1/agents | Agent 市场列表 | JWT |
| POST | /v1/agents | 创建 Agent | JWT |
| POST | /v1/agents/:id/purchase | 购买 Agent | JWT |
| GET | /v1/admin/stats | 管理后台仪表盘 | 管理员 JWT |

完整响应格式遵循统一包装：
```json
{
  "code": 0,
  "message": "success",
  "data": { }
}
```

## 与 Android App 联通

Android App 通过 `EleballApiClient` 连接后端。已在 `android/app/build.gradle.kts` 中配置 `BACKEND_BASE_URL`：

- **Debug**：`http://10.0.2.2:8080`（模拟器访问宿主机）
- **Release**：`https://api.eleball.cn/v1`

> 真机调试时，请将 debug 的 `BACKEND_BASE_URL` 改为宿主机局域网 IP，如 `http://192.168.1.19:8080`。

确保后端服务启动后，在 Android Studio 中以 Debug 模式运行 App，即可通过 `BuildConfig.BACKEND_BASE_URL` 自动连接到本地后端。

## 测试

```bash
cd gateway
make test
```

核心模块（auth、billing、chat、admin）已包含单元测试，覆盖率目标 > 60%。

## 管理后台

后端自带 React 管理后台，构建产物位于 `admin-web/dist/`。管理后台默认**不会自动暴露**，需要通过以下任一方式显式开启：

### 方式一：配置文件

编辑 `configs/config.yaml`：

```yaml
admin:
  enabled: true
```

### 方式二：环境变量

```bash
ADMIN_ENABLED=true ./build/eleball-gateway
```

### 方式三：命令行调试开关（推荐临时开启）

```bash
./build/eleball-gateway --enable-admin
```

开启后访问：

```
http://localhost:8080/admin/
```

> 生产环境建议保持 `admin.enabled: false`，仅在需要运维时通过 `--enable-admin` 或 `ADMIN_ENABLED=true` 临时开启，避免管理后台接口被扫描暴露。

### 运行时动态开关

管理后台开启后，可在不重启服务的情况下动态关闭或重新开启：

```bash
# 关闭管理后台（所有 /v1/admin/* 立即返回 404）
curl -X POST http://127.0.0.1:8080/_internal/admin/off

# 重新开启
curl -X POST http://127.0.0.1:8080/_internal/admin/on

# 查看状态
curl -X POST http://127.0.0.1:8080/_internal/admin/status
```

`/_internal/admin/*` 仅允许本机访问。若使用 `deployments/start.sh` Docker 部署，可使用封装脚本：

```bash
cd deployments
./admin-control.sh off
./admin-control.sh on
./admin-control.sh status
```

如需开发前端：
```bash
cd gateway/admin-web
npm install
npm run build
```
