# Eleball Web

> claw 用户端 H5，P2 本地化推进中。当前 web 尚未本地化，将在 P2 阶段完成 baseURL 双通道、技能页登录态拉云端、claw-guide 页等本地化工作。

## 开发

```bash
cd gateway/web
npm install
npm run dev      # http://localhost:5174
```

开发时 Vite 将 `/api` 代理到本地网关。

## 环境变量

| 变量 | 说明 |
|------|------|
| `VITE_API_BASE` | API 基础路径（默认 `/api`） |
| `VITE_CLOUD_BASE` | 云端站点地址（本地化双通道用） |
| `VITE_CLOUD_API` | 云端 API 地址（本地化双通道用） |

## 构建

```bash
npm run build    # 产物输出到 dist/
```

或使用仓库根的 `dev-run.sh` / `dev-run.ps1`，自动构建 web 与 admin-web 的 dist 并由 claw-server 提供页面。

## E2E 测试

基于 Playwright，位于 `e2e/` 目录：

```bash
npm run e2e:install   # 首次安装浏览器
npm run e2e           # 运行
npm run e2e:ui        # 交互调试
```

## 关联

- 本仓库根 README：`../../README.md`
- claw 实现规划（P2 web 本地化）：主仓库 `docs/marketing/claw-implementation-plan.md`
