import { memo, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkBreaks from 'remark-breaks'
import {
  Bot,
  CheckCircle,
  XCircle,
  Loader2,
  AlertTriangle,
  AlertCircle,
  Brain,
  ChevronDown,
  Download,
  Wrench
} from 'lucide-react'
import { agentApi } from '../api/client'

const markdownComponents = {
  p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
  ul: ({ children }) => <ul className="list-disc pl-5 mb-2 last:mb-0">{children}</ul>,
  ol: ({ children }) => <ol className="list-decimal pl-5 mb-2 last:mb-0">{children}</ol>,
  li: ({ children }) => <li className="mb-0.5">{children}</li>,
  a: ({ href, children }) => (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="text-eleball-primary hover:underline break-all"
    >
      {children}
    </a>
  ),
  code: ({ inline, className, children }) => {
    if (inline) {
      return (
        <code className="bg-gray-100 text-gray-800 px-1 py-0.5 rounded text-xs font-mono">
          {children}
        </code>
      )
    }
    const lang = (className || '').replace('language-', '')
    return (
      <pre className="bg-gray-100 rounded-lg p-2 overflow-x-auto text-xs my-2">
        {lang && <div className="text-[10px] text-eleball-text-tertiary mb-1 uppercase">{lang}</div>}
        <code className="font-mono whitespace-pre">{children}</code>
      </pre>
    )
  }
}

function formatValue(value) {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function ThinkingStep({ content }) {
  return (
    <div className="flex gap-2">
      <Brain className="w-3.5 h-3.5 mt-0.5 text-purple-500 flex-shrink-0" />
      <div className="text-xs text-eleball-text-secondary italic whitespace-pre-wrap">
        {content || <span className="text-eleball-text-tertiary">思考中…</span>}
      </div>
    </div>
  )
}

function IntermediateStep({ content }) {
  return (
    <div className="flex gap-2">
      <Bot className="w-3.5 h-3.5 mt-0.5 text-eleball-text-tertiary flex-shrink-0" />
      <div className="text-xs text-eleball-text-tertiary whitespace-pre-wrap">{content}</div>
    </div>
  )
}

function ToolStep({ step }) {
  const [expanded, setExpanded] = useState(false)
  const status = step.status || 'running'
  const statusIcon =
    status === 'running' ? (
      <Loader2 className="w-3.5 h-3.5 animate-spin text-eleball-primary" />
    ) : status === 'succeeded' ? (
      <CheckCircle className="w-3.5 h-3.5 text-green-600" />
    ) : status === 'failed' ? (
      <XCircle className="w-3.5 h-3.5 text-red-600" />
    ) : (
      <Wrench className="w-3.5 h-3.5 text-eleball-text-tertiary" />
    )

  return (
    <div
      className={`rounded-lg border text-xs transition-colors ${
        status === 'running'
          ? 'border-eleball-primary/30 bg-eleball-primary-light/20'
          : status === 'succeeded'
          ? 'border-green-200 bg-green-50'
          : status === 'failed'
          ? 'border-red-200 bg-red-50'
          : 'border-eleball-outline-variant bg-eleball-surface'
      }`}
    >
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center justify-between gap-2 px-2.5 py-2"
      >
        <span className="flex items-center gap-2 min-w-0">
          {statusIcon}
          <span className="font-medium truncate">{step.tool}</span>
        </span>
        <ChevronDown
          className={`w-3.5 h-3.5 text-eleball-text-tertiary flex-shrink-0 transition-transform ${
            expanded ? 'rotate-180' : ''
          }`}
        />
      </button>
      {expanded && (
        <div className="px-2.5 pb-2.5 space-y-2 text-eleball-text-secondary border-t border-eleball-outline-variant/50 pt-2">
          {step.arguments !== undefined && step.arguments !== null && (
            <div>
              <div className="text-[10px] uppercase tracking-wider text-eleball-text-tertiary mb-0.5">
                参数
              </div>
              <pre className="bg-white/60 rounded p-1.5 overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px]">
                {formatValue(step.arguments)}
              </pre>
            </div>
          )}
          {step.output !== undefined && step.output !== null && (
            <div>
              <div className="text-[10px] uppercase tracking-wider text-eleball-text-tertiary mb-0.5">
                结果
              </div>
              <pre className="bg-white/60 rounded p-1.5 overflow-x-auto whitespace-pre-wrap break-all font-mono text-[11px]">
                {formatValue(step.output)}
              </pre>
            </div>
          )}
          {step.error && (
            <div className="text-red-600 whitespace-pre-wrap text-[11px]">{step.error}</div>
          )}
        </div>
      )}
    </div>
  )
}

function AnswerStep({ content }) {
  // 与普通对话模式保持一致：不保留原始换行，由 remarkBreaks 统一处理段落换行
  return (
    <div className="text-eleball-text">
      <ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks]} components={markdownComponents}>
        {content || ''}
      </ReactMarkdown>
    </div>
  )
}

function ResourceStep({ resource }) {
  return (
    <a
      href={agentApi.getResource(resource.resource_id)}
      download
      target="_blank"
      rel="noreferrer"
      className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-eleball-surface border border-eleball-outline text-xs text-eleball-text hover:bg-eleball-surface-variant transition-colors"
    >
      <Download className="w-3.5 h-3.5" />
      <span className="max-w-[200px] truncate">{resource.file_name || '下载文件'}</span>
    </a>
  )
}

function WarningStep({ message }) {
  return (
    <div className="flex items-start gap-1.5 text-xs text-amber-600 bg-amber-50 rounded-lg px-2.5 py-2">
      <AlertTriangle className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
      <span className="whitespace-pre-wrap">{message}</span>
    </div>
  )
}

function ErrorStep({ message }) {
  return (
    <div className="flex items-start gap-1.5 text-xs text-red-600 bg-red-50 rounded-lg px-2.5 py-2">
      <AlertCircle className="w-3.5 h-3.5 mt-0.5 flex-shrink-0" />
      <span className="whitespace-pre-wrap">{message}</span>
    </div>
  )
}

function StepItem({ step }) {
  switch (step.type) {
    case 'thinking':
      return <ThinkingStep content={step.content} />
    case 'intermediate':
      return <IntermediateStep content={step.content} />
    case 'tool_call':
    case 'tool_result':
      return <ToolStep step={step} />
    case 'answer':
      return <AnswerStep content={step.content} />
    case 'resource':
      return <ResourceStep resource={step} />
    case 'warning':
      return <WarningStep message={step.message} />
    case 'error':
      return <ErrorStep message={step.message} />
    default:
      return null
  }
}

function AgentSteps({ steps, loading }) {
  if (!steps || steps.length === 0) {
    if (!loading) return null
    return (
      <div className="flex items-center gap-2 text-eleball-text-secondary text-xs">
        <Loader2 className="w-3.5 h-3.5 animate-spin text-eleball-primary" />
        <span>Agent 正在思考…</span>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {steps.map((step, idx) => {
        // Agent Team P5：子任务进度（sessionId 非空）缩进 + 左边框，与主循环步骤视觉分组
        const isSub = !!step.sessionId
        const showLabel = isSub && (!steps[idx - 1] || !steps[idx - 1].sessionId)
        return (
          <div key={`${step.type}-${idx}`} className={isSub ? 'ml-3 pl-3 border-l-2 border-eleball-primary/30' : ''}>
            {showLabel && <div className="text-[10px] text-eleball-primary/70 mb-0.5">↳ 委派子任务进度</div>}
            <StepItem step={step} />
          </div>
        )
      })}
      {loading && (
        <div className="flex items-center gap-2 text-eleball-text-secondary text-xs">
          <Loader2 className="w-3.5 h-3.5 animate-spin text-eleball-primary" />
          <span>继续生成中…</span>
        </div>
      )}
    </div>
  )
}

export default memo(AgentSteps)
