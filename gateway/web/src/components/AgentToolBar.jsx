import { Wrench, CheckCircle, XCircle } from 'lucide-react'

export function AgentToolBar({ toolSteps }) {
  if (!toolSteps || toolSteps.length === 0) return null

  const allDone = toolSteps.every(s => s.status === 'succeeded' || s.status === 'failed')
  const hasFailed = toolSteps.some(s => s.status === 'failed')

  return (
    <div className="flex items-center gap-2 text-xs text-eleball-text-secondary bg-eleball-surface-variant rounded-full px-3 py-1.5">
      <Wrench className="w-3.5 h-3.5" />
      <span>已调用 {toolSteps.length} 个工具</span>
      {allDone && (hasFailed ? (
        <XCircle className="w-3.5 h-3.5 text-red-600" />
      ) : (
        <CheckCircle className="w-3.5 h-3.5 text-green-600" />
      ))}
    </div>
  )
}
