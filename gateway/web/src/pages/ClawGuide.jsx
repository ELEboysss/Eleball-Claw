import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import useSEO from '../hooks/useSEO'
import { Monitor, Terminal, Shield, Cloud, HardDrive, Boxes, ArrowRight, BookOpen, CreditCard } from 'lucide-react'

// claw-guide：Eleball-Claw 用户使用指南。
// 面向最终用户：介绍产品定位、特点与使用要点，不涉及技术实现细节。
export default function ClawGuide() {
  useSEO('Eleball-Claw 使用指南', '把 AI agent 运行在你的设备上：安装、启动、账户、秘技使用指南。', true)

  useEffect(() => { document.title = 'Claw 使用指南 - Eleball' }, [])

  return (
    <div className="flex-1 bg-eleball-bg">
      {/* Hero */}
      <section className="pt-24 pb-10 px-4">
        <div className="max-w-4xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-eleball-primary-light text-eleball-primary-dark text-sm font-medium mb-6">
            <Monitor className="w-4 h-4" />
            设备端本地化
          </div>
          <h1 className="text-4xl font-bold text-eleball-text mb-4">Eleball-Claw 使用指南</h1>
          <p className="text-lg text-eleball-text-secondary leading-relaxed max-w-2xl mx-auto">
            Claw 把 AI agent 能力运行在你自己的设备上：对话数据留在本地、自主可控，同时与云端 Eleball 的账户、秘技、充值互通。
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
            <h2 className="text-xl font-bold text-eleball-text">本地与云端如何分工</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary mb-4">
            无需手动切换，Claw 会自动分流：涉及你数据的功能在本地完成，涉及账户与交易的功能走云端。
          </p>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div className="rounded-xl border border-eleball-outline-variant p-4">
              <div className="flex items-center gap-2 mb-2 text-eleball-primary font-semibold text-sm">
                <HardDrive className="w-4 h-4" /> 在你的设备上完成
              </div>
              <ul className="text-sm text-eleball-text-secondary space-y-1 list-disc list-inside">
                <li>对话与 Agent 任务，记录保存在本地</li>
                <li>图片 / 视频生成</li>
                <li>模型配置管理</li>
                <li>秘技的运行</li>
              </ul>
            </div>
            <div className="rounded-xl border border-eleball-outline-variant p-4">
              <div className="flex items-center gap-2 mb-2 text-eleball-primary font-semibold text-sm">
                <Cloud className="w-4 h-4" /> 在云端完成
              </div>
              <ul className="text-sm text-eleball-text-secondary space-y-1 list-disc list-inside">
                <li>账号登录 / 注册（与云端同一账号）</li>
                <li>充值 / VIP / 兑换码</li>
                <li>秘技购买与已购秘技拉取</li>
                <li>Ele Agent 模型调用计费</li>
              </ul>
            </div>
          </div>
        </section>

        {/* 安装运行 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Terminal className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">安装与启动</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary mb-3">
            一个命令完成安装，官方秘技随安装自带、开箱即用。
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
              <div className="text-xs text-eleball-text-tertiary mb-1">启动（Linux / macOS）</div>
              <pre className="bg-[#1e1e2e] text-gray-100 text-sm font-mono p-3 rounded-lg overflow-x-auto">eleball-claw serve --port=8090</pre>
            </div>
          </div>
          <p className="text-sm text-eleball-text-secondary mt-3">
            Windows 下也可以直接双击 <code className="text-xs bg-eleball-surface-variant px-1.5 py-0.5 rounded">eleball-claw.exe</code> 启动。
            启动后在浏览器打开 <code className="text-xs bg-eleball-surface-variant px-1.5 py-0.5 rounded">http://localhost:8090</code> 即可使用。
          </p>
        </section>

        {/* 统一账户 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Shield className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">一个账号，两端通用</h2>
          </div>
          <ul className="text-sm text-eleball-text-secondary space-y-2 list-disc list-inside leading-relaxed">
            <li>Claw 与云端 Eleball 使用同一账号登录，无需重复注册。</li>
            <li>本地对话不计费；使用 Ele Agent 模型时按云端账户余额计费，余额不足可在充值页充值。</li>
            <li>已购秘技、VIP 权益在两端同步生效。</li>
            <li>在控制台添加「Ele Agent 云端代理」后，模型调用自动使用你的登录态，登录态过期也无需手动更新 Key。</li>
          </ul>
        </section>

        {/* 秘技安装 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Boxes className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">秘技安装</h2>
          </div>
          <p className="text-sm text-eleball-text-secondary mb-3">技能页统一展示三个来源的秘技：</p>
          <ul className="text-sm text-eleball-text-secondary space-y-2 list-disc list-inside">
            <li><strong className="text-eleball-text">官方预置</strong>：随 Claw 安装自带的秘技（如联网搜索），开箱即用。</li>
            <li><strong className="text-eleball-text">云端已购</strong>：登录后可看到已购秘技，点「安装到本地」，自动完成安全校验后激活。</li>
            <li><strong className="text-eleball-text">本地自部署</strong>：你自己部署的秘技，也可「提交审核」上架到云端市场。</li>
          </ul>
          <p className="text-xs text-eleball-text-tertiary mt-3">
            未登录时仅展示本地秘技，登录后即可拉取云端已购内容。
          </p>
        </section>

        {/* 安全 */}
        <section className="card p-6">
          <div className="flex items-center gap-2 mb-4">
            <Shield className="w-5 h-5 text-eleball-primary" />
            <h2 className="text-xl font-bold text-eleball-text">数据安全</h2>
          </div>
          <ul className="text-sm text-eleball-text-secondary space-y-2 list-disc list-inside">
            <li>对话记录保存在你自己的设备上，不上传云端。</li>
            <li>模型 API Key 加密保存在本地。</li>
            <li>第三方秘技激活前会进行安全校验，来源不可靠的不会被启用。</li>
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
