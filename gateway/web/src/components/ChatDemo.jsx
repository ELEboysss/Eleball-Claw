import { useEffect, useRef, useState } from 'react'
import { motion, AnimatePresence } from 'framer-motion'
import { Bot, User, Image as ImageIcon, Sparkles } from 'lucide-react'

const script = [
  { role: 'user', content: '帮我总结一下这张截图的关键信息。', delay: 600 },
  { role: 'assistant', content: '截图里是一份 Q2 产品数据：DAU 提升 18%、付费转化率 4.2%，核心增长点在 SubAgent 工作流。', delay: 2200 },
  { role: 'user', content: '把这段结论润色成发给团队的日报。', delay: 3400 },
  { role: 'assistant', content: '好的，已生成日报：\n\n📊 Q2 日报\n- DAU 环比增长 18%，用户粘性持续改善。\n- 付费转化率达 4.2%，SubAgent 工作流成为新增长引擎。\n- 建议下周重点优化移动端悬浮球入口。', delay: 5200 },
  { role: 'user', content: '再帮我安排一次云南 5 日游。', delay: 6400 },
  { role: 'assistant', content: '正在调用旅行 SubAgent…', delay: 7600, agent: true },
  { role: 'assistant', content: '已为你规划好云南 5 日行程：D1 昆明→大理古城，D2 环洱海，D3 喜洲→丽江，D4 玉龙雪山，D5 束河古镇返程。需要我把预算和酒店建议一起整理吗？', delay: 9200 },
]

const lastMessageEndDelay =
  script[script.length - 1].delay + (script[script.length - 1].role === 'assistant' ? 1200 : 0)

export default function ChatDemo() {
  const [messages, setMessages] = useState([])
  const [typing, setTyping] = useState(false)
  const [cycle, setCycle] = useState(0)
  const containerRef = useRef(null)

  useEffect(() => {
    // 每次循环开始时清空，准备重新播放
    setMessages([])
    setTyping(false)
    const timers = []

    script.forEach((item) => {
      const t = setTimeout(() => {
        if (item.role === 'assistant') {
          setTyping(true)
          const replyTimer = setTimeout(() => {
            setTyping(false)
            setMessages((prev) => [...prev, item])
          }, item.agent ? 900 : 1200)
          timers.push(replyTimer)
        } else {
          setMessages((prev) => [...prev, item])
        }
      }, item.delay)
      timers.push(t)
    })

    // 全部消息展示完毕后，停留 4 秒再进入下一轮动画
    const restartTimer = setTimeout(() => {
      setCycle((c) => c + 1)
    }, lastMessageEndDelay + 4000)
    timers.push(restartTimer)

    return () => timers.forEach(clearTimeout)
  }, [cycle])

  useEffect(() => {
    const el = containerRef.current
    if (el) {
      el.scrollTop = el.scrollHeight
    }
  }, [messages, typing])

  return (
    <section id="demo" className="py-20 px-4 overflow-hidden">
      <div className="max-w-4xl mx-auto">
        <div className="text-center mb-14">
          <h2 className="text-3xl font-bold text-eleball-text mb-3">随时随地开始Chat</h2>
          <p className="text-eleball-text-secondary">对话、分析、SubAgent 一气呵成</p>
        </div>

        <motion.div
          initial={{ opacity: 0, y: 24 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true }}
          transition={{ duration: 0.6 }}
          className="mx-auto max-w-xl rounded-[2rem] border border-eleball-outline-variant bg-eleball-surface shadow-xl overflow-hidden"
        >
          {/* Window header */}
          <div className="flex items-center gap-3 px-5 py-4 border-b border-eleball-outline-variant bg-eleball-surface-variant/50">
            <div className="w-10 h-10 rounded-full bg-eleball-primary-light flex items-center justify-center">
              <Sparkles className="w-5 h-5 text-eleball-primary" />
            </div>
            <div>
              <div className="text-sm font-semibold text-eleball-text">Eleball 对话</div>
              <div className="text-xs text-eleball-text-tertiary">网页端 / App 端实时同步</div>
            </div>
          </div>

          {/* Messages */}
          <div
            ref={containerRef}
            className="h-96 px-5 py-6 overflow-y-auto bg-eleball-bg space-y-4"
          >
            <AnimatePresence initial={false}>
              {messages.map((msg, idx) => (
                <motion.div
                  key={`${cycle}-${idx}`}
                  initial={{ opacity: 0, y: 12, scale: 0.96 }}
                  animate={{ opacity: 1, y: 0, scale: 1 }}
                  exit={{ opacity: 0 }}
                  transition={{ duration: 0.35 }}
                  className={`flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}
                >
                  <div
                    className={`flex gap-3 max-w-[85%] ${msg.role === 'user' ? 'flex-row-reverse' : 'flex-row'}`}
                  >
                    <div
                      className={`w-8 h-8 rounded-full flex items-center justify-center shrink-0 ${
                        msg.role === 'user' ? 'bg-eleball-primary' : 'bg-eleball-surface-variant'
                      }`}
                    >
                      {msg.role === 'user' ? (
                        <User className="w-4 h-4 text-white" />
                      ) : (
                        <Bot className="w-4 h-4 text-eleball-primary" />
                      )}
                    </div>
                    <div
                      className={`px-4 py-3 rounded-2xl text-sm leading-relaxed whitespace-pre-wrap ${
                        msg.role === 'user'
                          ? 'bg-eleball-primary text-white rounded-tr-sm'
                          : 'bg-eleball-surface-variant text-eleball-text rounded-tl-sm border border-eleball-outline-variant'
                      }`}
                    >
                      {msg.content}
                    </div>
                  </div>
                </motion.div>
              ))}
            </AnimatePresence>

            {typing && (
              <motion.div
                initial={{ opacity: 0, y: 8 }}
                animate={{ opacity: 1, y: 0 }}
                className="flex justify-start"
              >
                <div className="flex gap-3 max-w-[85%]">
                  <div className="w-8 h-8 rounded-full bg-eleball-surface-variant flex items-center justify-center shrink-0">
                    <Bot className="w-4 h-4 text-eleball-primary" />
                  </div>
                  <div className="px-4 py-3 rounded-2xl rounded-tl-sm bg-eleball-surface-variant border border-eleball-outline-variant">
                    <div className="flex gap-1.5">
                      <span
                        className="w-2 h-2 rounded-full bg-eleball-primary/60 animate-bounce"
                        style={{ animationDelay: '0ms' }}
                      />
                      <span
                        className="w-2 h-2 rounded-full bg-eleball-primary/60 animate-bounce"
                        style={{ animationDelay: '150ms' }}
                      />
                      <span
                        className="w-2 h-2 rounded-full bg-eleball-primary/60 animate-bounce"
                        style={{ animationDelay: '300ms' }}
                      />
                    </div>
                  </div>
                </div>
              </motion.div>
            )}
          </div>

          {/* Input bar */}
          <div className="px-5 py-4 border-t border-eleball-outline-variant bg-eleball-surface">
            <div className="flex items-center gap-3 px-4 py-3 rounded-full bg-eleball-surface-variant border border-eleball-outline-variant">
              <ImageIcon className="w-5 h-5 text-eleball-text-tertiary" />
              <div className="flex-1 text-sm text-eleball-text-tertiary">输入问题，或发送截图…</div>
              <div className="w-8 h-8 rounded-full bg-eleball-primary flex items-center justify-center">
                <Sparkles className="w-4 h-4 text-white" />
              </div>
            </div>
          </div>
        </motion.div>
      </div>
    </section>
  )
}
