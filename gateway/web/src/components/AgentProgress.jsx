import { Loader2 } from 'lucide-react'

export function AgentProgress({ status }) {
  if (status !== 'executing' && status !== 'answering') return null

  return (
    <div className="flex items-center gap-2 text-xs text-eleball-text-secondary">
      <Loader2 className="w-3.5 h-3.5 animate-spin" />
      <span>{status === 'executing' ? '工具执行中…' : '生成回答中…'}</span>
    </div>
  )
}
