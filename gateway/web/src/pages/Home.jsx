import { useState } from 'react'
import { Link } from 'react-router-dom'
import { motion } from 'framer-motion'
import { MessageSquare, Image, Text, Workflow, Shield, Download } from 'lucide-react'
import useSEO from '../hooks/useSEO'
import DownloadModal from '../components/DownloadModal'
import LoginModal from '../components/LoginModal'
import ChatDemo from '../components/ChatDemo'
import { useAuth } from '../context/AuthContext'

const features = [
  {
    icon: Image,
    title: '截图分析',
    desc: '截取当前屏幕，一键总结、翻译、解释或提取关键信息。'
  },
  {
    icon: Text,
    title: '选中文本处理',
    desc: '复制任意文字，悬浮球即时响应翻译、润色、解释等快捷指令。'
  },
  {
    icon: MessageSquare,
    title: '多轮对话',
    desc: '保留上下文，支持语音输入与附件，随时切换已配置模型。'
  },
  {
    icon: Workflow,
    title: 'SubAgent 工作流',
    desc: '复杂任务自动拆解为多个子代理并行执行，结果汇总返回。'
  }
]

const models = [
  { name: 'GPT-5.4', provider: 'OpenAI', tags: ['多模态', '推理'] },
  { name: 'Claude Opus 4.7', provider: 'Anthropic', tags: ['代码', 'Agent'] },
  { name: 'Gemini 3.1 Pro', provider: 'Google', tags: ['长上下文', '多模态'] },
  { name: 'DeepSeek-V4', provider: 'DeepSeek', tags: ['推理', '高性价比'] },
  { name: 'Kimi K2.7', provider: 'Moonshot', tags: ['长文本', '多模态'] },
  { name: 'Qwen3.7-Max', provider: '通义千问', tags: ['中文强', '全能'] },
  { name: 'GLM-5.1', provider: '智谱 AI', tags: ['代码', 'Agent'] },
  { name: '...', provider: '其他海量大模型', tags: ['持续接入中'] }
]

export default function Home() {
  useSEO('Eleball - 悬浮球 AI 助手，双击看懂屏幕', '全局悬浮球 AI 助手，双击截图分析、复制即翻译、多模型自由切换。不用切 App，隐私优先。', true)
  const [downloadOpen, setDownloadOpen] = useState(false)
  const [loginOpen, setLoginOpen] = useState(false)
  const { isLoggedIn } = useAuth()

  return (
    <div className="pt-16">
      {/* Hero */}
      <section className="px-4 py-20 sm:py-28 text-center">
        <motion.div
          initial={{ opacity: 0, scale: 0.9 }}
          animate={{ opacity: 1, scale: 1 }}
          transition={{ duration: 0.6 }}
          className="mx-auto w-28 h-28 mb-8"
        >
          <img
            src="/logo-icon.png"
            alt="Eleball"
            className="w-full h-full object-contain drop-shadow-lg"
          />
        </motion.div>

        <motion.h1
          initial={{ opacity: 0, y: 16 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.1, duration: 0.6 }}
          className="text-4xl sm:text-5xl font-bold text-eleball-text mb-4"
        >
          别再把截图发来发去了，这个球自己就能看懂。
        </motion.h1>

        <motion.p
          initial={{ opacity: 0, y: 16 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.2, duration: 0.6 }}
          className="max-w-2xl mx-auto text-lg text-eleball-text-secondary mb-8"
        >
          Eleball 是常驻屏幕边缘的悬浮球。单击对话、双击截图分析、长按快捷指令--全程不用切 App。
        </motion.p>

        <motion.div
          initial={{ opacity: 0, y: 16 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.3, duration: 0.6 }}
          className="flex flex-col sm:flex-row items-center justify-center gap-4"
        >
          {isLoggedIn ? (
            <Link to="/chat" className="btn-primary text-base px-8 py-4">
              免费开始对话
            </Link>
          ) : (
            <button
              onClick={() => setLoginOpen(true)}
              className="btn-primary text-base px-8 py-4"
            >
              免费开始对话
            </button>
          )}
          <button
            onClick={() => setDownloadOpen(true)}
            className="btn-secondary text-base px-8 py-4"
          >
            <Download className="w-5 h-5" />
            下载 Android 版
          </button>
        </motion.div>
      </section>

      <DownloadModal open={downloadOpen} onClose={() => setDownloadOpen(false)} />
      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />

      {/* Features */}
      <section id="features" className="py-20 px-4 bg-eleball-surface-variant/50">
        <div className="max-w-6xl mx-auto">
          <div className="text-center mb-14">
            <h2 className="text-3xl font-bold text-eleball-text mb-3">零摩擦的 AI 体验</h2>
            <p className="text-eleball-text-secondary">随时随地，一点即问</p>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6">
            {features.map((feature, idx) => (
              <motion.div
                key={feature.title}
                initial={{ opacity: 0, y: 24 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={{ once: true }}
                transition={{ delay: idx * 0.1, duration: 0.5 }}
                className="card"
              >
                <div className="w-12 h-12 rounded-2xl bg-eleball-primary-light flex items-center justify-center mb-4">
                  <feature.icon className="w-6 h-6 text-eleball-primary" />
                </div>
                <h3 className="text-lg font-semibold text-eleball-text mb-2">{feature.title}</h3>
                <p className="text-sm text-eleball-text-secondary leading-relaxed">{feature.desc}</p>
              </motion.div>
            ))}
          </div>
        </div>
      </section>

      {/* Models */}
      <section id="models" className="py-20 px-4 bg-eleball-surface-variant/30">
        <div className="max-w-6xl mx-auto">
          <div className="text-center mb-14">
            <h2 className="text-3xl font-bold text-eleball-text mb-3">多模型自由选择</h2>
            <p className="text-eleball-text-secondary">支持 BYOK 与 Ele Agent 代调用，灵活切换</p>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6">
            {models.map((model, idx) => (
              <motion.div
                key={model.name}
                initial={{ opacity: 0, y: 24 }}
                whileInView={{ opacity: 1, y: 0 }}
                viewport={{ once: true }}
                transition={{ delay: idx * 0.1, duration: 0.5 }}
                className="card text-center"
              >
                <h3 className={`font-bold text-eleball-text mb-1 ${model.name === '...' ? 'text-4xl' : 'text-xl'}`}>{model.name}</h3>
                <p className="text-sm text-eleball-text-secondary mb-4">{model.provider}</p>
                {model.name !== '...' && (
                  <div className="flex flex-wrap justify-center gap-2">
                    {model.tags.map((tag) => (
                      <span key={tag} className="px-2.5 py-1 rounded-lg text-xs font-medium text-eleball-primary bg-eleball-primary-light">
                        {tag}
                      </span>
                    ))}
                  </div>
                )}
              </motion.div>
            ))}
          </div>
        </div>
      </section>

      {/* Chat Demo */}
      <ChatDemo />

      {/* Trust */}
      <section className="py-20 px-4">
        <div className="max-w-4xl mx-auto text-center">
          <div className="w-16 h-16 rounded-2xl bg-eleball-surface-variant flex items-center justify-center mx-auto mb-6">
            <Shield className="w-8 h-8 text-eleball-primary" />
          </div>
          <h2 className="text-3xl font-bold text-eleball-text mb-4">你的数据，你做主</h2>
          <div className="max-w-2xl mx-auto space-y-3 text-left">
            <p className="flex items-start gap-3 text-eleball-text-secondary leading-relaxed">
              <span className="text-eleball-primary mt-1">✓</span>
              <span>你的 Key 只在本地加密存储，连我们服务器都碰不到。</span>
            </p>
            <p className="flex items-start gap-3 text-eleball-text-secondary leading-relaxed">
              <span className="text-eleball-primary mt-1">✓</span>
              <span>截图分析前，先问你同不同意上传。</span>
            </p>
            <p className="flex items-start gap-3 text-eleball-text-secondary leading-relaxed">
              <span className="text-eleball-primary mt-1">✓</span>
              <span>看视频、打游戏、进银行 App，悬浮球会自动藏起来。</span>
            </p>
          </div>
          <p className="mt-8 inline-block px-4 py-2 rounded-full text-sm font-medium text-eleball-primary bg-eleball-primary-light">
            Android 内测进行中 · 前 1000 名用户后续有小惊喜
          </p>
        </div>
      </section>

      {/* FAQ */}
      <section className="py-20 px-4 bg-eleball-surface-variant/30">
        <div className="max-w-3xl mx-auto">
          <div className="text-center mb-12">
            <h2 className="text-3xl font-bold text-eleball-text mb-3">常见问题</h2>
            <p className="text-eleball-text-secondary">先回答你最可能想问的</p>
          </div>
          <div className="space-y-6">
            <div>
              <h3 className="text-lg font-semibold text-eleball-text mb-2">这玩意儿会不会一直盯着我屏幕？</h3>
              <p className="text-eleball-text-secondary leading-relaxed">不会。默认 BYOK 模式，截图前会弹二次确认；你看视频、打游戏、进银行 App 时它会自动藏起来；API Key 本地加密，服务器碰不到。</p>
            </div>
            <div>
              <h3 className="text-lg font-semibold text-eleball-text mb-2">有 iOS 版吗？</h3>
              <p className="text-eleball-text-secondary leading-relaxed">iOS 正在开发中，阶段二上线。可以先关注动态蹲一波。</p>
            </div>
            <div>
              <h3 className="text-lg font-semibold text-eleball-text mb-2">怎么收费？</h3>
              <p className="text-eleball-text-secondary leading-relaxed">自带 API Key 的模型完全免费，我们不经手你的费用。用官方 Ele Agent 模型按 token 扣弹丸，¥9.9 起。VIP1 ¥49/月解锁 Agent 模式与文件工具。</p>
            </div>
            <div>
              <h3 className="text-lg font-semibold text-eleball-text mb-2">和我手机自带的 AI 有什么不同？</h3>
              <p className="text-eleball-text-secondary leading-relaxed">系统 AI 模型不可选、不感知屏幕、无 Agent 生态。Eleball 是开放可扩展的系统级入口--悬浮球直达、看见屏幕、多模型自由切换。</p>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}
