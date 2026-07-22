import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import useSEO from '../hooks/useSEO'
import { Monitor, Terminal, Shield, Cloud, HardDrive, Boxes, ArrowRight, BookOpen, CreditCard } from 'lucide-react'

// claw-guide：Eleball-claw 本地使用指南。
// claw 是部署在设备端的本地化组件，本页说明它是什么、如何安装运行、本地/云端双通道如何工作、
// 秘技如何安装。云端官网/充值已改为本地内嵌页（/、/recharge）；文档等内容在官网 iframe 内导航到达。
export default function ClawGuide() {
  useSEO('Eleball-claw 使用指南', '本地化 Eleball：安装、运行、双通道、秘技安装指南。', true)

  useEffect(() => { document.title = 'claw 使用指南 - Eleball' }, [])

  return (
    <div className="flex-1 bg-eleball-bg">
      {/* Hero */}
      <section className="pt-24 pb-10 px-4">
        <div className="max-w-4xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-eleball-primary-light text-eleball-primary-dark text-sm font-medium mb-6">
            <Monitor className="w-4 h-4" />
            设备端本地化
          </div>
          <h1 className="text-4xl font-bold text-eleball-text mb-4">Eleball-claw 使用指南</h1>
          <p className="text-lg text-eleball-text-secondary leading-relaxed max-w-2xl mx-auto">
            claw 把云端 Eleball 的 agent 能力搬到你的设备本地：数据与编排自主可控，同时与云端账户、秘技、文档、充值互通。
          </p>
          <div className="flex flex-wrap items-center justify-center gap-3 mt-6">
            <Link to="/chat" className="btn-primary text-sm px-5 py-2 inline-flex items-center gap-2">
              开始对话 <ArrowRight className="w-4 h-4" />
            </Link>
            <Link to="/" className="text-sm px-5 py-2 inline-flex items-center gap-2 rounded-full border border-eleball-outline-variant text-eleball-text-secondary hover:text-eleball-text">
              <Cloud className="w-4 h-4" /> 前往官网
            </Link>
          </div>
        </div>
      </section>

      <div className="max-w-4xl mx-auto px-4 pb-24 space-y-10">
        {/* 本地 vs 云端 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Boxes className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">本地与云端的分工</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary mb-4">
            claw 网关处理本地能力，云端 eleball 处理账户与交易。前端按接口自动分流：
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="rounded-xl border border-eleball-outline-variant p-4">
              <div className="flex items-center gap-2 mb-2 text-eleball-primary font-semibold text-sm">
                <HardDrive className="w-4 h-4" /> 本地 claw gateway
              </div>
              <ul className="text-sm text-eleball-text-secondary space-y-1 list-disc list-inside">
                <li>对话 / Agent 工作流（数据本地存储）</li>
                <li>视觉生成（图片 / 视频）</li>
                <li>模型配置（本地化）</li>
                <li>秘技模块运行（本地扫描 + 已安装）</li>
                <li>对话历史同步</li>
              </ul>
            </div>
            <div className="rounded-xl border border-eleball-outline-variant p-4">
              <div className="flex items-center gap-2 mb-2 text-eleball-primary font-semibold text-sm">
                <Cloud className="w-4 h-4" /> 云端 eleball
              </div>
              <ul className="text-sm text-eleball-text-secondary space-y-1 list-disc list-inside">
                <li>账户登录 / 注册（统一账户）</li>
                <li>充值 / 支付 / VIP / 兑换码</li>
                <li>秘技购买与已购拉取</li>
                <li>官网 / 文档 / 充值页内容</li>
                <li>Ele Agent 模型计费（经 BaseURL 转发）</li>
              </ul>
            </div>
          </div>
        </section>

        {/* 安装运行 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Terminal className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">安装与运行</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary mb-3">
            单文件二进制，少依赖（无 CGO、内置 SQLite）。官方模块预置免容器。
          </p>
          <div className="space-y-3">
            <div>
              <div className="text-xs text-eleball-text-tertiary mb-1">Linux / macOS</div>
              <pre className="bg-[#1e1e2e] text-gray-100 text-sm font-mono p-3 rounded-lg overflow-x-auto">curl -fsSL https://eleball.cn/install.sh | sh</pre>
            </div>
            <div>
              <div className="text-xs text-eleball-text-tertiary mb-1">Windows（PowerShell）</div>
              <pre className="bg-[#1e1e2e] text-gray-100 text-sm font-mono p-3 rounded-lg overflow-x-auto">irm https://eleball.cn/install.ps1 | iex</pre>
            </div>
            <div>
              <div className="text-xs text-eleball-text-tertiary mb-1">启动</div>
              <pre className="bg-[#1e1e2e] text-gray-100 text-sm font-mono p-3 rounded-lg overflow-x-auto">eleball-claw serve --port=8090</pre>
            </div>
          </div>
          <p className="text-xs text-eleball-text-tertiary mt-3">
            安装脚本待 P6 发布正式二进制后可用；当前可从源码编译（见仓库 README）。
          </p>
        </section>

        {/* 统一账户 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Shield className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">统一账户</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary leading-relaxed">
            claw 与云端共享同一套账户：登录走云端，一个 Token 两端通用。
            为此需将 claw 的 <code className="text-xs bg-eleball-surface-variant px-1.5 py-0.5 rounded">JWT_SECRET</code>
            配置为与云端一致（环境变量注入），使云端签发的 Token 在 claw 本地校验通过。
            本地对话不计费；使用 Ele Agent 模型时，请求经 BaseURL 转发至云端
            <code className="text-xs bg-eleball-surface-variant px-1.5 py-0.5 rounded mx-1">api.eleball.cn/v1</code>
            由云端账户扣费。
          </p>
        </section>

        {/* 秘技安装 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Boxes className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">秘技安装</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary mb-3">技能页合并三个来源，统一展示：</p>
          <ul className="text-sm text-eleball-text-secondary space-y-2 list-disc list-inside">
            <li><strong className="text-eleball-text">官方预置</strong>：随 claw 分发的模块（如联网搜索），开箱即用，无需拉镜像。</li>
            <li><strong className="text-eleball-text">云端已购</strong>：登录后从云端拉取已购秘技元数据，点「安装到本地」激活。官方模块直接激活，第三方模块拉取容器镜像并校验签名后激活。</li>
            <li><strong className="text-eleball-text">本地自部署</strong>：开发者本地实现的模块，可「提交审核」转云端上架。</li>
          </ul>
          <p className="text-xs text-eleball-text-tertiary mt-3">
            未登录时仅展示本地自部署模块与驱动，页面会提示「若需要更多秘技，请登录账号」。
          </p>
        </section>

        {/* 安全 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Shield className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">安全</h2>
          </div>
          <ul className="text-sm text-eleball-text-secondary space-y-2 list-disc list-inside">
            <li>本地数据 SQLite，数据不出设备。</li>
            <li>第三方模块镜像需签名校验（cosign / sigstore）通过方可激活。</li>
            <li>API Key 用 AES-256-GCM 加密入库，请求期间仅驻内存。</li>
          </ul>
        </section>

        {/* 云端入口 */}
        <section className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Link to="/" className="card p-5 text-left hover:shadow-md transition-shadow">
            <BookOpen className="w-5 h-5 text-eleball-primary mb-2" />
            <div className="font-semibold text-eleball-text text-sm mb-1">官网</div>
            <div className="text-xs text-eleball-text-secondary">产品介绍 / 文档 / 充值等</div>
          </Link>
          <Link to="/recharge" className="card p-5 text-left hover:shadow-md transition-shadow">
            <CreditCard className="w-5 h-5 text-eleball-primary mb-2" />
            <div className="font-semibold text-eleball-text text-sm mb-1">充值</div>
            <div className="text-xs text-eleball-text-secondary">弹丸 / VIP / 兑换码</div>
          </Link>
        </section>
      </div>
    </div>
  )
}
