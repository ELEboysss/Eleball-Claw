import { useState, useEffect } from 'react'
import useSEO from '../hooks/useSEO'
import { BookOpen, Code, MessageSquare, Shield, Zap, Smartphone, CreditCard, Ticket, ChevronRight, Copy, Check, Monitor, Bot, Package, Store } from 'lucide-react'

function CodeBlock({ code, language = 'bash' }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = () => {
    navigator.clipboard.writeText(code)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="relative group rounded-xl bg-[#1e1e2e] overflow-hidden my-4">
      <div className="flex items-center justify-between px-4 py-2 bg-[#252536] text-xs text-gray-400">
        <span>{language}</span>
        <button
          onClick={handleCopy}
          className="flex items-center gap-1 hover:text-white transition-colors"
        >
          {copied ? <Check className="w-3.5 h-3.5" /> : <Copy className="w-3.5 h-3.5" />}
          {copied ? '已复制' : '复制'}
        </button>
      </div>
      <pre className="p-4 overflow-x-auto text-sm text-gray-100 font-mono leading-relaxed">
        <code>{code}</code>
      </pre>
    </div>
  )
}

function SectionTitle({ icon: Icon, title, subtitle }) {
  return (
    <div className="mb-8">
      <div className="flex items-center gap-2 mb-2">
        {Icon && <Icon className="w-5 h-5 text-eleball-primary" />}
        <h2 className="text-2xl font-bold text-eleball-text">{title}</h2>
      </div>
      {subtitle && <p className="text-eleball-text-secondary">{subtitle}</p>}
    </div>
  )
}

function FeatureCard({ icon: Icon, title, desc }) {
  return (
    <div className="card p-6 hover:shadow-md transition-shadow">
      <div className="w-10 h-10 rounded-xl bg-eleball-primary-light flex items-center justify-center mb-4">
        <Icon className="w-5 h-5 text-eleball-primary" />
      </div>
      <h3 className="font-semibold text-eleball-text mb-2">{title}</h3>
      <p className="text-sm text-eleball-text-secondary leading-relaxed">{desc}</p>
    </div>
  )
}

function Endpoint({ method, path, desc }) {
  return (
    <div className="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-4 py-3 border-b border-eleball-outline-variant last:border-0">
      <span className="inline-flex items-center px-2.5 py-1 rounded-md text-xs font-semibold bg-emerald-50 text-emerald-700 w-fit">
        {method}
      </span>
      <code className="text-sm font-mono text-eleball-text">{path}</code>
      <span className="text-sm text-eleball-text-secondary sm:ml-auto">{desc}</span>
    </div>
  )
}

function DtoTable({ children }) {
  return (
    <div className="overflow-x-auto my-4 rounded-xl border border-eleball-outline-variant">
      <table className="w-full text-sm text-left border-collapse">
        <thead className="bg-eleball-surface-variant text-eleball-text-secondary">
          <tr>
            <th className="px-4 py-2.5 font-medium w-1/5">字段</th>
            <th className="px-4 py-2.5 font-medium w-1/6">类型</th>
            <th className="px-4 py-2.5 font-medium w-1/6">必填</th>
            <th className="px-4 py-2.5 font-medium">说明</th>
          </tr>
        </thead>
        <tbody className="text-eleball-text divide-y divide-eleball-outline-variant">{children}</tbody>
      </table>
    </div>
  )
}

function DtoRow({ field, type, required, children }) {
  return (
    <tr>
      <td className="px-4 py-3 font-mono text-eleball-primary">{field}</td>
      <td className="px-4 py-3 text-eleball-text-secondary">{type}</td>
      <td className="px-4 py-3 text-eleball-text-secondary">{required}</td>
      <td className="px-4 py-3 text-eleball-text-secondary leading-relaxed">{children}</td>
    </tr>
  )
}

function ContentPartCard({ type, fields, example }) {
  return (
    <div className="card p-4 border border-eleball-outline-variant">
      <div className="flex items-center gap-2 mb-2">
        <code className="text-sm font-mono text-eleball-primary">{type}</code>
      </div>
      <ul className="text-sm text-eleball-text-secondary space-y-1 mb-3">
        {fields.map((f) => (
          <li key={f.name}>
            <code className="text-xs bg-eleball-surface-variant px-1.5 py-0.5 rounded">{f.name}</code>
            <span className="text-xs text-eleball-text-tertiary ml-2">{f.type}{f.required ? ' · 必填' : ''}</span>
            <p className="mt-0.5">{f.desc}</p>
          </li>
        ))}
      </ul>
      <CodeBlock code={example} language="json" />
    </div>
  )
}

export default function Docs() {
  useSEO('使用与接入文档', 'Eleball 使用指南、API 接入、开发者指南。')
  const [activeTab, setActiveTab] = useState('web')
  const [activeSection, setActiveSection] = useState('')
  const baseUrl = 'https://api.eleball.cn/v1'

  // 根据当前 Tab 生成本页目录
  const tocItems = activeTab === 'app'
    ? [
        { id: 'app-features', label: '手机 App 核心能力' },
        { id: 'app-notes', label: '手机端使用须知' },
        { id: 'use-cases', label: '适用场景' },
        { id: 'help', label: '需要帮助？' },
      ]
    : [
        { id: 'web-features', label: 'Web 端与 API 核心能力' },
        { id: 'chat-mode', label: 'Chat 模式' },
        { id: 'agent-mode', label: 'Agent 模式' },
        { id: 'quickstart', label: '开发者快速接入' },
        { id: 'api-overview', label: 'Web 端 API 概览' },
        { id: 'auth-dto', label: '认证接口 DTO' },
        { id: 'chat-agent-dto', label: '对话与 Agent 接口 DTO' },
        { id: 'multimodal', label: '多模态调用示例' },
        { id: 'examples', label: '常用示例' },
        { id: 'creator', label: '秘技开发与上架' },
        { id: 'web-notes', label: 'Web 端使用须知' },
        { id: 'use-cases', label: '适用场景' },
        { id: 'help', label: '需要帮助？' },
      ]

  // 滚动监听，高亮当前目录
  useEffect(() => {
    const handleScroll = () => {
      const scrollPos = window.scrollY + 120
      let current = ''
      tocItems.forEach((item) => {
        const el = document.getElementById(item.id)
        if (el && el.offsetTop <= scrollPos) {
          current = item.id
        }
      })
      setActiveSection(current || tocItems[0]?.id)
    }
    handleScroll()
    window.addEventListener('scroll', handleScroll, { passive: true })
    return () => window.removeEventListener('scroll', handleScroll)
  }, [tocItems])

  const scrollToSection = (id) => {
    const el = document.getElementById(id)
    if (el) {
      window.scrollTo({ top: el.offsetTop - 80, behavior: 'smooth' })
    }
  }

  const chatCode = `curl ${baseUrl}/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN" \\
  -d '{
    "provider": "eleagent",
    "model": "qwen/Qwen/Qwen3-8B",
    "messages": [
      {"role": "user", "content": "你好，请介绍一下 Eleball"}
    ],
    "stream": true
  }'`

  const balanceCode = `curl ${baseUrl}/billing/balance \\
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN"`

  const agentExecuteCode = `curl ${baseUrl}/agent/execute \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN" \\
  -d '{
    "conversation_id": "conv_xxx",
    "message": "帮我查一下今天的新闻",
    "history": [
      {"role": "user", "content": "帮我查一下今天的新闻"}
    ],
    "model": "qwen/Qwen/Qwen3-8B",
    "provider": "eleagent",
    "enable_tools": true,
    "enable_web_search": true,
    "search_provider": "baidu"
  }'`

  return (
    <div className="flex-1 bg-eleball-bg">
      {/* Hero */}
      <section className="pt-24 pb-12 px-4">
        <div className="max-w-4xl mx-auto text-center">
          <div className="inline-flex items-center gap-2 px-4 py-2 rounded-full bg-eleball-primary-light text-eleball-primary-dark text-sm font-medium mb-6">
            <BookOpen className="w-4 h-4" />
            开发者与用户文档
          </div>
          <h1 className="text-4xl font-bold text-eleball-text mb-4">
            Eleball 使用与接入文档
          </h1>
          <p className="text-lg text-eleball-text-secondary leading-relaxed max-w-2xl mx-auto">
            Eleball 提供手机端 AI 助手与 Web 端及 API 应用两种使用形态。请根据你使用的终端或接入方式查看对应功能说明。
          </p>
        </div>
      </section>

      {/* Tab Switcher */}
      <div className="max-w-4xl mx-auto px-4 mb-8">
        <div className="flex p-1 rounded-xl bg-eleball-surface-variant border border-eleball-outline-variant w-fit mx-auto">
          <button
            onClick={() => setActiveTab('app')}
            className={`flex items-center gap-2 px-6 py-2.5 rounded-lg text-sm font-medium transition-all ${
              activeTab === 'app'
                ? 'bg-white text-eleball-primary shadow-sm'
                : 'text-eleball-text-secondary hover:text-eleball-text'
            }`}
          >
            <Smartphone className="w-4 h-4" />
            手机 App
          </button>
          <button
            onClick={() => setActiveTab('web')}
            className={`flex items-center gap-2 px-6 py-2.5 rounded-lg text-sm font-medium transition-all ${
              activeTab === 'web'
                ? 'bg-white text-eleball-primary shadow-sm'
                : 'text-eleball-text-secondary hover:text-eleball-text'
            }`}
          >
            <Monitor className="w-4 h-4" />
            Web 端与 API 应用
          </button>
        </div>
      </div>

      <div className="max-w-7xl mx-auto px-4 pb-24 flex gap-8">
        <main className="flex-1 max-w-4xl mx-auto xl:mx-0 space-y-16">
          {/* App Tab */}
        {activeTab === 'app' && (
          <>
            <section id="app-features" className="scroll-mt-24">
              <SectionTitle
                icon={Smartphone}
                title="手机 App 核心能力"
                subtitle="Android 客户端提供的原生 AI 助手能力"
              />
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <FeatureCard
                  icon={Smartphone}
                  title="全局悬浮球"
                  desc="在任意界面通过悬浮球快速触发对话小窗，无需切换应用即可开始 AI 交互。"
                />
                <FeatureCard
                  icon={MessageSquare}
                  title="对话小窗"
                  desc="支持多轮对话、模型切换、流式输出，悬浮于当前应用之上，随时可收起。"
                />
                <FeatureCard
                  icon={Zap}
                  title="屏幕上下文捕获"
                  desc="支持截图、选中文本等方式将当前屏幕内容作为上下文发送给 AI 进行分析或问答。"
                />
                <FeatureCard
                  icon={Code}
                  title="BYOK 与自定义模型"
                  desc="除 Ele Agent 模型外，可添加自有 API Key 的第三方模型，数据由模型服务商处理。"
                />
                <FeatureCard
                  icon={Shield}
                  title="本地安全存储"
                  desc="手机端 API Key 等敏感信息使用系统密钥库加密存储，截图分析等操作需用户二次确认。"
                />
                <FeatureCard
                  icon={CreditCard}
                  title="计费"
                  desc="使用 Ele Agent 模型时按调用消耗弹丸，可通过充值或兑换码补充余额。"
                />
              </div>
            </section>

            <section id="app-notes" className="scroll-mt-24">
              <SectionTitle
                icon={Shield}
                title="手机端使用须知"
                subtitle="开始使用前请了解以下事项"
              />
              <div className="card p-6 space-y-4 text-sm text-eleball-text-secondary">
                <p>
                  <strong className="text-eleball-text">权限说明：</strong>
                  悬浮球需要悬浮窗权限，屏幕捕获需要录屏/截图权限，用户可在系统设置中随时关闭。
                </p>
                <p>
                  <strong className="text-eleball-text">模型选择：</strong>
                  默认使用 Ele Agent 模型，无需配置 Key；如需使用自定义模型，请在设置页添加对应 API Key。
                </p>
                <p>
                  <strong className="text-eleball-text">数据安全：</strong>
                  用户对话内容与截图仅在必要时发送至所选模型服务商。具体数据处理与留存策略请参考隐私政策。
                </p>
              </div>
            </section>
          </>
        )}

        {/* Web Tab */}
        {activeTab === 'web' && (
          <>
            <section id="web-features" className="scroll-mt-24">
              <SectionTitle
                icon={Monitor}
                title="Web 端与 API 应用核心能力"
                subtitle="浏览器端与 API 开发者可直接使用的功能"
              />
              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <FeatureCard
                  icon={MessageSquare}
                  title="在线对话"
                  desc="通过浏览器访问 /chat 即可与 Ele Agent 模型进行流式对话，支持模型切换与余额查看。"
                />
                <FeatureCard
                  icon={Code}
                  title="Ele Agent 模型列表"
                  desc="在 /models 查看当前 Eleball 支持接入的大模型，按平台筛选，了解各模型计费标准。"
                />
                <FeatureCard
                  icon={CreditCard}
                  title="充值"
                  desc="在 /recharge 查看余额、选择充值套餐，或通过兑换码为账户充值弹丸。"
                />
                <FeatureCard
                  icon={Ticket}
                  title="兑换码充值"
                  desc="通过 CDK 兑换码为账户充值弹丸，适用于活动分发、团队配额管理等场景。"
                />
                <FeatureCard
                  icon={Code}
                  title="OpenAI 兼容 API"
                  desc="开发者可通过 API 调用 Ele Agent 模型，请求与响应格式兼容 OpenAI 协议。"
                />
                <FeatureCard
                  icon={Shield}
                  title="账户安全"
                  desc="Access Token 与 Refresh Token 机制保障登录安全，自定义模型所需的 API Key 按平台最佳实践存储。"
                />
              </div>
            </section>

            <section id="chat-mode" className="scroll-mt-24">
              <SectionTitle
                icon={MessageSquare}
                title="Chat 模式"
                subtitle="标准流式对话，适合问答、写作、编程与多模态理解"
              />
              <div className="card p-6 space-y-4">
                <p className="text-sm text-eleball-text-secondary leading-relaxed">
                  Chat 模式通过 <code>/chat/completions</code> 与模型进行单轮或多轮对话，请求与响应兼容 OpenAI 协议。
                  支持 SSE 流式输出，可传入文本、图片、文件等上下文，按实际调用消耗弹丸。
                  适合日常问答、内容创作、代码解释、文档分析等场景。
                </p>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">核心接口</h4>
                  <Endpoint method="POST" path="/chat/completions" desc="代理对话，兼容 OpenAI 格式，支持流式输出" />
                </div>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">适用场景</h4>
                  <ul className="text-sm text-eleball-text-secondary list-disc list-inside space-y-1">
                    <li>多轮闲聊、知识问答与摘要总结</li>
                    <li>文案写作、翻译、润色与代码解释</li>
                    <li>图片/文本文件上下文分析（需模型支持）</li>
                  </ul>
                </div>
              </div>
            </section>

            <section id="agent-mode" className="scroll-mt-24">
              <SectionTitle
                icon={Bot}
                title="Agent 模式"
                subtitle="多步工具调用，支持联网搜索、文件处理与任务拆解"
              />
              <div className="card p-6 space-y-4">
                <p className="text-sm text-eleball-text-secondary leading-relaxed">
                  Agent 模式在对话基础上启用工具循环。模型可根据用户需求自动调用 SearchWeb、FetchURL、文件处理等工具，
                  并支持多轮思考与中间结果展示。普通用户每天有 3 次免费试用，VIP 用户无限制。
                  适合需要实时信息、复杂分析或生成交付物的任务。
                </p>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">核心接口</h4>
                  <Endpoint method="POST" path="/agent/execute" desc="启动 Agent 工作流，SSE 返回工具调用、思考与最终回答" />
                  <Endpoint method="GET" path="/agent/search-providers" desc="获取当前可用的联网搜索源列表" />
                  <Endpoint method="GET" path="/agent/sessions" desc="查询当前用户的 Agent Session 列表" />
                  <Endpoint method="GET" path="/agent/sessions/:id" desc="获取指定 Session 详情与工具链记录" />
                  <Endpoint method="GET" path="/agent/resources/:id" desc="匿名下载 Agent 生成的可下载资源" />
                </div>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">SSE 事件说明</h4>
                  <DtoTable>
                    <DtoRow field="tool_call" type="event" required="-">模型决定调用某个工具，包含 step、tool、arguments。</DtoRow>
                    <DtoRow field="tool_result" type="event" required="-">工具执行结果，包含 step、status、output、error_message。</DtoRow>
                    <DtoRow field="reasoning" type="event" required="-">模型的思考过程（delta 字段增量返回）。</DtoRow>
                    <DtoRow field="intermediate_answer" type="event" required="-">中间说明文字（delta 字段增量返回）。</DtoRow>
                    <DtoRow field="final_answer" type="event" required="-">最终回答内容（delta 字段增量返回）。</DtoRow>
                    <DtoRow field="resource" type="event" required="-">可下载资源信息，包含 resource_id、file_name、download_url。</DtoRow>
                    <DtoRow field="error" type="event" required="-">执行过程中发生的错误。</DtoRow>
                    <DtoRow field="done" type="event" required="-">工作流结束标记。</DtoRow>
                  </DtoTable>
                </div>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">适用场景</h4>
                  <ul className="text-sm text-eleball-text-secondary list-disc list-inside space-y-1">
                    <li>需要联网搜索最新信息的问答与报告生成</li>
                    <li>多步骤数据处理、代码运行与文件转换</li>
                    <li>生成可下载资源（如报告、代码包）的自动化任务</li>
                  </ul>
                </div>
              </div>
            </section>

            <section id="quickstart" className="scroll-mt-24">
              <SectionTitle
                icon={Code}
                title="开发者快速接入"
                subtitle="通过 Eleball API 将模型能力集成到你的应用中"
              />
              <div className="card p-6 space-y-6">
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2 flex items-center gap-2">
                    <span className="w-6 h-6 rounded-full bg-eleball-primary text-white text-xs flex items-center justify-center">1</span>
                    获取访问凭证
                  </h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    在 Eleball Web 端或 App 完成注册登录后，系统会下发 Access Token 与 Refresh Token。
                    调用 API 时在请求头中携带 <code>Authorization: Bearer {'<access_token>'}</code>。
                  </p>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2 flex items-center gap-2">
                    <span className="w-6 h-6 rounded-full bg-eleball-primary text-white text-xs flex items-center justify-center">2</span>
                    选择 Base URL
                  </h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    Eleball 网关 API Base URL 为 <code>{baseUrl}</code>，所有接口路径均在此前缀下。
                    根据你的接入场景，可选择以下入口：
                  </p>
                  <ul className="text-sm text-eleball-text-secondary list-disc list-inside space-y-1 mb-3">
                    <li><strong>App / 第三方 API 接入：</strong><code>https://api.eleball.cn/v1</code>（推荐移动端与外部应用调用）</li>
                    <li><strong>Web 端 / 管理后台接入：</strong><code>https://eleball.cn/api</code>（与官网同源调用时使用）</li>
                    <li><strong>服务健康检查：</strong><code>https://api.eleball.cn/health</code> 或 <code>https://eleball.cn/health</code></li>
                  </ul>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2 flex items-center gap-2">
                    <span className="w-6 h-6 rounded-full bg-eleball-primary text-white text-xs flex items-center justify-center">3</span>
                    发起对话请求
                  </h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    以下示例展示如何调用 Ele Agent 模型进行流式对话：
                  </p>
                  <CodeBlock code={chatCode} language="curl" />
                </div>
              </div>
            </section>

            <section id="api-overview" className="scroll-mt-24">
              <SectionTitle
                icon={BookOpen}
                title="Web 端 API 概览"
                subtitle="当前 Eleball 网关面向用户与开发者开放的接口"
              />
              <div className="card p-6">
                <Endpoint method="POST" path="/auth/register" desc="用户注册，创建 Eleball 账户并下发 Token" />
                <Endpoint method="POST" path="/auth/login" desc="用户登录，使用邮箱和密码换取 Token" />
                <Endpoint method="POST" path="/auth/refresh" desc="刷新 Access Token" />
                <Endpoint method="GET" path="/auth/me" desc="获取当前登录用户信息" />
                <Endpoint method="POST" path="/chat/completions" desc="代理对话，兼容 OpenAI 格式，支持流式输出" />
                <Endpoint method="POST" path="/agent/execute" desc="Agent 工作流，SSE 返回工具调用与最终回答" />
                <Endpoint method="GET" path="/agent/search-providers" desc="获取可用联网搜索源" />
                <Endpoint method="GET" path="/agent/sessions" desc="查询 Agent Session 列表" />
                <Endpoint method="GET" path="/agent/sessions/:id" desc="获取 Agent Session 详情" />
                <Endpoint method="GET" path="/agent/resources/:id" desc="下载 Agent 生成的资源" />
                <Endpoint method="GET" path="/billing/balance" desc="查询当前账户弹丸余额" />
                <Endpoint method="GET" path="/billing/recharge-history" desc="查询当前账户充值记录" />
                <Endpoint method="GET" path="/eleagent/models" desc="获取当前可用的 Ele Agent 模型列表" />
              </div>
              <p className="text-xs text-eleball-text-tertiary mt-3">
                完整 OpenAPI 契约见 <code>specs/api-schema.yml</code>，以下为最常用的接入 DTO 说明。
              </p>
            </section>

            <section id="auth-dto" className="scroll-mt-24">
              <SectionTitle
                icon={Shield}
                title="认证接口 DTO"
                subtitle="注册、登录与刷新 Token 的请求字段说明"
              />
              <div className="card p-6 space-y-8">
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">POST /auth/register</h3>
                  <DtoTable>
                    <DtoRow field="username" type="string" required="是">用户名，最少 3 个字符。</DtoRow>
                    <DtoRow field="password" type="string" required="是">登录密码，最少 6 个字符。</DtoRow>
                    <DtoRow field="device_id" type="string" required="是">设备唯一标识，用于多设备登录管理。</DtoRow>
                  </DtoTable>
                </div>
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">POST /auth/login</h3>
                  <DtoTable>
                    <DtoRow field="username" type="string" required="是">注册时的用户名。</DtoRow>
                    <DtoRow field="password" type="string" required="是">登录密码。</DtoRow>
                    <DtoRow field="device_id" type="string" required="是">设备唯一标识。</DtoRow>
                  </DtoTable>
                </div>
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">POST /auth/refresh</h3>
                  <DtoTable>
                    <DtoRow field="refresh_token" type="string" required="是">登录或注册接口返回的 Refresh Token。</DtoRow>
                  </DtoTable>
                </div>
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">响应字段（TokenPair）</h3>
                  <DtoTable>
                    <DtoRow field="access_token" type="string" required="-">访问令牌，调用受保护接口时使用。</DtoRow>
                    <DtoRow field="refresh_token" type="string" required="-">刷新令牌，用于 Access Token 过期后换取新令牌。</DtoRow>
                    <DtoRow field="user_id" type="string" required="-">当前用户 ID。</DtoRow>
                    <DtoRow field="default_model_profile" type="object" required="-">默认 Ele Agent 模型配置。</DtoRow>
                  </DtoTable>
                </div>
              </div>
            </section>

            <section id="chat-agent-dto" className="scroll-mt-24">
              <SectionTitle
                icon={Code}
                title="对话与 Agent 接口 DTO"
                subtitle="/chat/completions 与 /agent/execute 的请求字段与调用示例"
              />
              <div className="card p-6 space-y-8">
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">POST /chat/completions 请求体</h3>
                  <DtoTable>
                    <DtoRow field="provider" type="string" required="是">
                      模型平台，如 <code>eleagent</code>、<code>openai</code>、<code>deepseek</code>、<code>moonshot</code>、<code>qwen</code>、<code>custom</code>。使用 Ele Agent 模型时必须显式指定。
                    </DtoRow>
                    <DtoRow field="model" type="string" required="是">
                      模型标识。Ele Agent 使用 <code>subProvider/subModel</code> 格式，如 <code>qwen/Qwen/Qwen3-8B</code>；直接调用上游时填写上游模型名，如 <code>gpt-4o</code>。
                    </DtoRow>
                    <DtoRow field="messages" type="ChatMessage[]" required="是">
                      对话消息数组，支持纯文本字符串或多模态 content parts 数组。
                    </DtoRow>
                    <DtoRow field="stream" type="boolean" required="否">
                      是否流式返回，默认 <code>true</code>。流式返回 SSE，非流式返回完整 JSON。
                    </DtoRow>
                    <DtoRow field="temperature" type="number" required="否">
                      采样温度，默认 <code>0.7</code>，范围 0~2。
                    </DtoRow>
                    <DtoRow field="top_p" type="number" required="否">
                      核采样参数，与 temperature 二选一。
                    </DtoRow>
                    <DtoRow field="max_tokens" type="integer" required="否">
                      单次回复最大 token 数。
                    </DtoRow>
                    <DtoRow field="max_completion_tokens" type="integer" required="否">
                      OpenAI / Kimi Code 兼容字段，与 max_tokens 语义一致。
                    </DtoRow>
                    <DtoRow field="thinking" type="object" required="否">
                      思考模型参数，如 <code>{`{"type":"enabled","keep":"medium"}`}</code>。
                    </DtoRow>
                    <DtoRow field="stop" type="string[]" required="否">
                      停止词列表。
                    </DtoRow>
                  </DtoTable>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">ChatMessage</h3>
                  <DtoTable>
                    <DtoRow field="role" type="string" required="是">
                      消息角色：<code>system</code>、<code>user</code>、<code>assistant</code>、<code>tool</code>。
                    </DtoRow>
                    <DtoRow field="content" type="string | ContentPart[]" required="是">
                      消息内容。纯文本时为 string；需要图片/文件上下文时为 ContentPart 数组。
                    </DtoRow>
                    <DtoRow field="name" type="string" required="否">
                      角色名，tool 消息或特殊场景使用。
                    </DtoRow>
                  </DtoTable>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">ContentPart 类型</h3>
                  <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                    <ContentPartCard
                      type="text"
                      fields={[{ name: 'text', type: 'string', required: true, desc: '文本内容' }]}
                      example={`{\n  "type": "text",\n  "text": "请解释这段代码"\n}`}
                    />
                    <ContentPartCard
                      type="image_url"
                      fields={[
                        { name: 'url', type: 'string', required: true, desc: '图片 URL，支持公开 URL 或 data:image/...;base64,... 内联数据' },
                        { name: 'detail', type: 'string', required: false, desc: '图片细节级别：auto / low / high，网关会按需透传' }
                      ]}
                      example={`{\n  "type": "image_url",\n  "image_url": {\n    "url": "data:image/png;base64,iVBORw0KGgo..."\n  }\n}`}
                    />
                    <ContentPartCard
                      type="file"
                      fields={[
                        { name: 'name', type: 'string', required: true, desc: '文件名' },
                        { name: 'mimeType', type: 'string', required: true, desc: '文件 MIME 类型' },
                        { name: 'text', type: 'string', required: false, desc: '文本类文件已提取的内容' },
                        { name: 'data', type: 'string', required: false, desc: '二进制文件内联数据，data:application/...;base64,... 形式' }
                      ]}
                      example={`{\n  "type": "file",\n  "file": {\n    "name": "main.go",\n    "mimeType": "text/plain",\n    "text": "package main\\n..."\n  }\n}`}
                    />
                  </div>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">响应说明</h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    <code>stream=true</code> 时返回 <code>text/event-stream</code>，每个 chunk 为 OpenAI 兼容格式：
                  </p>
                  <CodeBlock
                    code={`data: {"id":"chatcmpl_eleball","object":"chat.completion.chunk","created":1710000000,"choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":null}]}

data: [DONE]`}
                    language="text"
                  />
                  <p className="text-sm text-eleball-text-secondary mt-4 mb-3">
                    <code>stream=false</code> 时返回完整 JSON：
                  </p>
                  <CodeBlock
                    code={`{
  "code": 0,
  "message": "success",
  "data": {
    "id": "chatcmpl_eleball",
    "model": "qwen/Qwen/Qwen3-8B",
    "choices": [{
      "index": 0,
      "message": { "role": "assistant", "content": "你好！有什么可以帮你的？" },
      "finish_reason": "stop"
    }],
    "usage": {
      "prompt_tokens": 12,
      "completion_tokens": 8,
      "total_tokens": 20
    }
  }
}`}
                    language="json"
                  />
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">错误码</h3>
                  <DtoTable>
                    <DtoRow field="400" type="HTTP" required="-">参数错误，如必填字段缺失或消息格式不正确。</DtoRow>
                    <DtoRow field="401" type="HTTP" required="-">未授权，Token 缺失、过期或无效。</DtoRow>
                    <DtoRow field="402" type="HTTP" required="-">Ele Agent 付费模型余额不足。</DtoRow>
                    <DtoRow field="500" type="HTTP" required="-">网关内部错误或上游模型调用失败。</DtoRow>
                  </DtoTable>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">POST /agent/execute</h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    <code>/agent/execute</code> 接受与 <code>/chat/completions</code> 类似的对话上下文，但会启用工具循环。
                    开启 <code>enable_web_search</code> 后模型可自动联网搜索。接口返回 <code>text/event-stream</code>，前端按 SSE 事件解析即可。
                  </p>
                  <CodeBlock code={agentExecuteCode} language="curl" />
                </div>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">/agent/execute 关键请求字段</h4>
                  <DtoTable>
                    <DtoRow field="conversation_id" type="string" required="否">对话 ID，为空时后端会自动创建新对话。</DtoRow>
                    <DtoRow field="message" type="string" required="是">当前用户输入。</DtoRow>
                    <DtoRow field="history" type="ChatMessage[]" required="否">历史消息数组，用于保持多轮上下文。</DtoRow>
                    <DtoRow field="model" type="string" required="是">Ele Agent 模型标识，如 <code>qwen/Qwen/Qwen3-8B</code>。</DtoRow>
                    <DtoRow field="provider" type="string" required="是">固定填 <code>eleagent</code>。</DtoRow>
                    <DtoRow field="enable_tools" type="boolean" required="否">是否启用工具调用，默认 <code>true</code>。</DtoRow>
                    <DtoRow field="enable_web_search" type="boolean" required="否">是否允许模型联网搜索，默认 <code>false</code>。</DtoRow>
                    <DtoRow field="search_provider" type="string" required="否">联网搜索源，如 <code>baidu</code>、<code>bing</code>。</DtoRow>
                    <DtoRow field="attachments" type="Attachment[]" required="否">附件列表，支持图片、文本文件等。</DtoRow>
                  </DtoTable>
                </div>
                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">/agent/execute SSE 响应片段示例</h4>
                  <CodeBlock
                    code={`event: tool_call\ndata: {"step":1,"tool":"SearchWeb","arguments":{"query":"今天的新闻"}}\n\nevent: tool_result\ndata: {"step":1,"tool":"SearchWeb","status":"succeeded","output":{"result":"..."}}\n\nevent: final_answer\ndata: {"delta":"今天的主要新闻有..."}\n\nevent: done\ndata: {}`}
                    language="text"
                  />
                </div>


              </div>
            </section>

            <section id="multimodal" className="scroll-mt-24">
              <SectionTitle
                icon={Code}
                title="多模态调用示例"
                subtitle="图片与文件上下文的使用方式"
              />
              <div className="card p-6 space-y-6">
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">图片上下文（需视觉模型支持）</h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    当前 Ele Agent 视觉模型如 <code>moonshot/moonshot-v1-8k-vision-preview</code>、<code>moonshot/kimi-k2.6</code> 支持图片理解。非视觉模型收到图片后会将其降级为文本上下文。
                  </p>
                  <CodeBlock
                    code={`curl ${baseUrl}/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN" \\
  -d '{
    "provider": "eleagent",
    "model": "moonshot/moonshot-v1-8k-vision-preview",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,iVBORw0KGgo..."}},
        {"type": "text", "text": "这张截图里写了什么？"}
      ]
    }],
    "stream": true
  }'`}
                    language="curl"
                  />
                </div>
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">文件上下文（文本文件）</h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    文本类文件直接提取内容作为上下文；二进制文件建议由调用方自行提取文本或说明。
                  </p>
                  <CodeBlock
                    code={`curl ${baseUrl}/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer YOUR_ACCESS_TOKEN" \\
  -d '{
    "provider": "eleagent",
    "model": "qwen/Qwen/Qwen3-8B",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "file", "file": {"name": "main.go", "mimeType": "text/plain", "text": "package main\\n..."}},
        {"type": "text", "text": "请解释这段代码"}
      ]
    }],
    "stream": true
  }'`}
                    language="curl"
                  />
                </div>
              </div>
            </section>

            <section id="examples" className="scroll-mt-24">
              <SectionTitle
                icon={Code}
                title="常用示例"
                subtitle="余额查询与模型列表"
              />
              <div className="card p-6 space-y-6">
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">查询余额</h3>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    调用余额接口可获取当前账户的弹丸（danwan）与优雅弹丸（elegant）余额。
                  </p>
                  <CodeBlock code={balanceCode} language="curl" />
                </div>
                <div>
                  <h3 className="font-semibold text-eleball-text mb-2">返回示例</h3>
                  <CodeBlock
                    code={`{
  "code": 0,
  "message": "success",
  "data": {
    "danwan": 5000,
    "elegant": 0,
    "unit": "cent"
  }
}`}
                    language="json"
                  />
                </div>
              </div>
            </section>

            <section id="creator" className="scroll-mt-24">
              <SectionTitle
                icon={Store}
                title="秘技开发与上架"
                subtitle="在 Eleball 弹丸集市发布和销售你的 AI 能力"
              />
              <div className="card p-6 space-y-8">
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                  <FeatureCard
                    icon={Bot}
                    title="Prompt 型秘技"
                    desc="只需编写 system prompt，无需代码即可上架。适合文案生成、风格转换、角色扮演等场景。"
                  />
                  <FeatureCard
                    icon={Package}
                    title="工具型秘技"
                    desc="调用网页阅读、全网搜索、视频字幕、社交平台等现有能力，或接入你的私有 API。"
                  />
                </div>

                <div className="space-y-4 text-sm text-eleball-text-secondary">
                  <p>
                    <strong className="text-eleball-text">开发流程：</strong>
                    准备 manifest.json、system prompt 和可选的输入输出 schema → 本地测试 → 打包为 .eleball-agent → 提交审核。
                  </p>
                  <p>
                    <strong className="text-eleball-text">定价方式：</strong>
                    支持免费、一次性弹丸购买、优雅弹丸订阅、按调用计费等多种模式。
                  </p>
                  <p>
                    <strong className="text-eleball-text">收益结算：</strong>
                    平台收取一定比例服务费后，剩余收益以优雅弹丸形式进入开发者账户，可申请提现。
                  </p>
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-3">工具型秘技标准接口</h3>
                  <p className="text-sm text-eleball-text-secondary mb-4">
                    工具型秘技通过一份 ToolManifest 描述自己，并由一个集市模块实际执行。模块只需实现两个标准 HTTP 接口，即可被 Eleball 网关发现、调用和上架。
                  </p>
                  <Endpoint method="GET" path="/health" desc="上报模块在线状态与能力清单" />
                  <Endpoint method="POST" path="/execute" desc="执行指定 action，返回工具调用结果" />
                </div>

                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">GET /health 响应示例</h4>
                  <CodeBlock
                    code={`{
  "module_id": "my-module",
  "version": "1.0.0",
  "status": "ok",
  "capabilities": ["web_read", "search"]
}`}
                    language="json"
                  />
                </div>

                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">POST /execute 请求与响应示例</h4>
                  <p className="text-sm text-eleball-text-secondary mb-3">
                    网关调用模块时传入 action、params 和当前 user_id。模块处理完成后返回 content 和可选的来源列表。
                  </p>
                  <CodeBlock
                    code={`// 请求
{
  "action": "web_read",
  "params": {
    "query": "https://example.com/article"
  },
  "user_id": "user_xxx"
}

// 响应
{
  "content": "文章正文摘要...",
  "sources": ["https://example.com/article"]
}`}
                    language="json"
                  />
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-3">ToolManifest 字段说明</h3>
                  <p className="text-sm text-eleball-text-secondary mb-4">
                    ToolManifest 是秘技的接口契约，用于向 Eleball 说明工具的参数、能力、权限与定价。
                  </p>
                  <DtoTable>
                    <DtoRow field="id" type="string" required="是">全局唯一 ID，建议反域名格式。</DtoRow>
                    <DtoRow field="name" type="string" required="是">展示名称，最多 64 字符。</DtoRow>
                    <DtoRow field="description" type="string" required="是">功能描述，会拼接到模型 function description。</DtoRow>
                    <DtoRow field="driver" type="string" required="是">执行驱动别名，如 agent_reach / firecrawl / builtin。新 SKU 应使用已注册的驱动别名，而非 module。</DtoRow>
                    <DtoRow field="runtime_type" type="string" required="否">运行时层级：builtin / wasm / sidecar / remote。</DtoRow>
                    <DtoRow field="category" type="string" required="是">分类，如 互联网、开发、办公、创意。</DtoRow>
                    <DtoRow field="level" type="integer" required="是">1-6，对应秘技等级。</DtoRow>
                    <DtoRow field="permissions" type="string[]" required="否">所需权限标签，如 network / file_tools / shell。</DtoRow>
                    <DtoRow field="parameters" type="object" required="是">OpenAI function calling 参数 schema。</DtoRow>
                    <DtoRow field="actions" type="object[]" required="是">支持的操作列表，每个操作包含 name 和 description。</DtoRow>
                    <DtoRow field="metadata.module" type="string" required="否">（兼容字段）显式声明依赖的集市模块 ID。新 SKU 请使用 driver 别名。</DtoRow>
                    <DtoRow field="credentials" type="object" required="否">需要用户预先配置的 Cookie / API Key / Token 声明。</DtoRow>
                    <DtoRow field="timeout_seconds" type="integer" required="否">单次调用超时秒数，0 表示使用全局默认。</DtoRow>
                  </DtoTable>
                </div>

                <div>
                  <h4 className="font-semibold text-eleball-text mb-2">ToolManifest 示例</h4>
                  <CodeBlock
                    code={`{
  "id": "com.example.web_reader",
  "name": "网页阅读器",
  "description": "读取任意网页正文并返回标题与摘要。",
  "driver": "agent_reach",
  "runtime_type": "remote",
  "category": "互联网",
  "level": 2,
  "permissions": ["network"],
  "parameters": {
    "type": "object",
    "properties": {
      "action": { "type": "string", "enum": ["web_read"] },
      "query": { "type": "string", "description": "要读取的网页 URL" }
    },
    "required": ["action", "query"]
  },
  "actions": [
    { "name": "web_read", "description": "读取网页正文" }
  ],
  "timeout_seconds": 60
}`}
                    language="json"
                  />
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-3">用户凭证注入</h3>
                  <p className="text-sm text-eleball-text-secondary mb-4">
                    如果你的秘技需要用户预先提供 Cookie、API Key 或 Token，在 ToolManifest 的 credentials 中声明。网关在调用模块前会自动注入到 params.credentials 中。
                  </p>
                  <CodeBlock
                    code={`{
  "credentials": {
    "api_key": {
      "type": "api_key",
      "label": "API Key",
      "description": "你的上游服务 API Key",
      "placeholder": "sk-...",
      "required": true
    }
  }
}

// 模块 /execute 中读取
{
  "action": "search",
  "params": {
    "query": "Eleball",
    "credentials": {
      "api_key": "sk-xxxxxxxx"
    }
  },
  "user_id": "user_xxx"
}`}
                    language="json"
                  />
                </div>

                <div>
                  <h3 className="font-semibold text-eleball-text mb-3">上架流程</h3>
                  <div className="space-y-4 text-sm text-eleball-text-secondary">
                    <p>
                      <strong className="text-eleball-text">1. 成为开发者：</strong>
                      在 Eleball App 或 Web 端注册账号，进入创作者中心申请成为开发者。
                    </p>
                    <p>
                      <strong className="text-eleball-text">2. 准备材料：</strong>
                      编写 manifest.json、system prompt（可选）和测试用例。如需自定义后端，部署符合标准接口的模块服务。
                    </p>
                    <p>
                      <strong className="text-eleball-text">3. 提交审核：</strong>
                      上传 .eleball-agent 包或在线编辑 manifest，选择定价方式后提交审核。
                    </p>
                    <p>
                      <strong className="text-eleball-text">4. 上架销售：</strong>
                      审核通过后，用户即可在弹丸集市搜索、购买并使用你的秘技。
                    </p>
                  </div>
                </div>

                <div className="text-sm text-eleball-text-secondary">
                  详细开发者手册见{' '}
                  <a
                    href="/docs/agent-market-creator-guide.md"
                    target="_blank"
                    rel="noreferrer"
                    className="text-eleball-primary hover:underline"
                  >
                    《Eleball 秘技开发与上架接入手册》
                  </a>
                  ；技术实现细节见{' '}
                  <a
                    href="/docs/tool-driver-guide.md"
                    target="_blank"
                    rel="noreferrer"
                    className="text-eleball-primary hover:underline"
                  >
                    《Tool Driver / Manifest 接入指南》
                  </a>
                  。
                </div>
              </div>
            </section>

            <section id="web-notes" className="scroll-mt-24">
              <SectionTitle
                icon={Shield}
                title="Web 端与 API 应用使用须知"
                subtitle="调用 API 前请了解以下事项"
              />
              <div className="card p-6 space-y-4 text-sm text-eleball-text-secondary">
                <p>
                  <strong className="text-eleball-text">认证方式：</strong>
                  所有需要登录的接口均通过 Bearer Token 认证，Token 过期后可使用 Refresh Token 换取新的 Access Token。
                </p>
                <p>
                  <strong className="text-eleball-text">计费单位：</strong>
                  余额以“弹丸”为单位，Ele Agent 模型按调用消耗弹丸，具体费率以模型列表返回为准。
                </p>
                <p>
                  <strong className="text-eleball-text">数据安全：</strong>
                  自定义模型所需的 API Key 按平台最佳实践存储。具体数据处理与留存策略请参考隐私政策。
                </p>
                <p>
                  <strong className="text-eleball-text">兼容性：</strong>
                  /chat/completions 接口在请求与响应格式上兼容 OpenAI 协议，便于现有应用迁移。
                </p>
              </div>
            </section>
          </>
        )}

        {/* Common Use Cases */}
        <section id="use-cases" className="scroll-mt-24">
          <SectionTitle
            icon={Zap}
            title="适用场景"
            subtitle="Eleball 可服务的典型场景"
          />
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            {[
              { title: '移动场景快速提问', desc: '通过手机悬浮球随时截图或选中文本，向 AI 提问而无需切换应用。' },
              { title: 'Web 端办公辅助', desc: '在浏览器中使用在线对话完成摘要、翻译、写作、代码解释等任务。' },
              { title: 'AI 应用原型开发', desc: '开发者通过 Eleball API 快速接入 Ele Agent 模型，验证产品创意。' },
              { title: '团队共享账户', desc: '通过充值套餐与兑换码管理团队成员的弹丸配额。' },
            ].map((item) => (
              <div key={item.title} className="card p-5">
                <h3 className="font-semibold text-eleball-text mb-2 flex items-center gap-2">
                  <ChevronRight className="w-4 h-4 text-eleball-primary" />
                  {item.title}
                </h3>
                <p className="text-sm text-eleball-text-secondary leading-relaxed">{item.desc}</p>
              </div>
            ))}
          </div>
        </section>

        {/* Contact */}
        <section id="help" className="text-center scroll-mt-24">
          <div className="card p-8">
            <h2 className="text-xl font-bold text-eleball-text mb-2">需要帮助？</h2>
            <p className="text-eleball-text-secondary text-sm mb-4">
              如果你在使用过程中遇到问题，或有商务合作需求，欢迎联系我们。
            </p>
            <a
              href="mailto:support@eleball.cn"
              className="inline-flex items-center gap-2 text-eleball-primary hover:text-eleball-primary-dark font-medium"
            >
              support@eleball.cn
            </a>
          </div>
        </section>
      </main>

      {/* 右侧目录导航 */}
      <aside className="hidden xl:block w-56 shrink-0">
        <div className="sticky top-24 max-h-[calc(100vh-8rem)] overflow-y-auto bg-eleball-surface rounded-2xl border border-eleball-outline-variant p-4 shadow-sm">
          <h3 className="text-xs font-bold text-eleball-text-secondary uppercase tracking-wider mb-3 px-2">
            本页目录
          </h3>
          <nav className="space-y-1">
            {tocItems.map((item) => (
              <button
                key={item.id}
                onClick={() => scrollToSection(item.id)}
                className={`w-full text-left px-2 py-1.5 rounded-lg text-sm transition-colors ${
                  activeSection === item.id
                    ? 'bg-eleball-primary-light text-eleball-primary-dark font-medium'
                    : 'text-eleball-text-secondary hover:text-eleball-text hover:bg-eleball-surface-variant'
                }`}
              >
                {item.label}
              </button>
            ))}
          </nav>
        </div>
      </aside>
      </div>
    </div>
  )
}
