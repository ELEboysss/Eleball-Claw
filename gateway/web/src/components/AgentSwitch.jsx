import { Bot, X } from 'lucide-react'

/**
 * Agent 模式切换按钮
 * 激活状态：蓝色药丸，带 X 可关闭
 * 未激活状态：浅色边框/文字，点击开启
 */
export default function AgentSwitch({ checked, onChange, disabled }) {
  return (
    <button
      type="button"
      onClick={() => !disabled && onChange(!checked)}
      disabled={disabled}
      title={disabled ? '当前为 BYOK 模型，Agent 工具仅对 Ele Agent 模型开放' : '启用后 AI 可自主调用工具辅助回答'}
      className={[
        'inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-full text-sm font-medium border transition-colors',
        checked
          ? 'bg-blue-50 text-blue-600 border-blue-200 hover:bg-blue-100'
          : 'bg-transparent text-eleball-text-secondary border-eleball-outline hover:bg-gray-50 hover:text-eleball-text',
        disabled ? 'opacity-50 cursor-not-allowed' : 'cursor-pointer'
      ].join(' ')}
    >
      <Bot className="w-4 h-4" />
      <span>Agent</span>
      {checked && !disabled && (
        <span
          className="ml-0.5 p-0.5 rounded-full hover:bg-blue-200/60"
          onClick={(e) => {
            e.stopPropagation()
            onChange(false)
          }}
          title="关闭 Agent"
        >
          <X className="w-3 h-3" />
        </span>
      )}
    </button>
  )
}
