import { X, Bot, Check, Settings as SettingsIcon } from 'lucide-react'
import { Link } from 'react-router-dom'

/**
 * 助手切换弹窗：选择本会话使用哪个助手（已激活秘技的命名组合）。
 * 与「切换模型」同款交互--输入区一个按钮触发、弹窗内切换，
 * 避免旧的自定义下拉浮层被输入框容器 overflow-hidden 裁切。
 */
export default function AssistantPicker({ open, onClose, assistants, currentId, onPick }) {
  if (!open) return null

  // currentId 为空字符串表示「默认（全部已激活）」
  const isActive = (id) => (id || '') === (currentId || '')
  const rowCls = (active) =>
    `w-full flex items-center gap-3 p-3 rounded-xl border transition-colors cursor-pointer text-left ${
      active
        ? 'border-eleball-primary bg-eleball-primary-light/30'
        : 'border-eleball-outline-variant hover:border-eleball-primary/50'
    }`

  return (
    <div
      className="fixed inset-0 z-[100] flex items-center justify-center px-4 bg-black/50"
      onClick={onClose}
    >
      <div
        className="relative w-full max-w-sm dialog-panel p-5 max-h-[80vh] overflow-y-auto"
        onClick={(e) => e.stopPropagation()}
      >
        <button
          onClick={onClose}
          className="absolute top-4 right-4 p-1 rounded-full text-eleball-text-tertiary hover:bg-eleball-surface-variant"
        >
          <X className="w-5 h-5" />
        </button>

        <h2 className="text-lg font-bold text-eleball-text mb-1">选择助手</h2>
        <p className="text-sm text-eleball-text-secondary mb-4">本会话调用的已激活秘技组合</p>

        <div className="space-y-2">
          <button
            type="button"
            onClick={() => {
              onPick('')
              onClose()
            }}
            className={rowCls(isActive(''))}
          >
            <div className="w-9 h-9 rounded-full flex items-center justify-center bg-eleball-primary-light shrink-0">
              <Bot className="w-5 h-5 text-eleball-primary" />
            </div>
            <div className="flex-1 min-w-0">
              <div className="text-sm font-medium text-eleball-text truncate">默认（全部已激活）</div>
              <div className="text-xs text-eleball-text-tertiary truncate">使用全部已激活秘技</div>
            </div>
            {isActive('') && <Check className="w-4 h-4 text-eleball-primary shrink-0" />}
          </button>

          {assistants.map((a) => {
            const active = isActive(a.id)
            return (
              <button
                key={a.id}
                type="button"
                onClick={() => {
                  onPick(a.id)
                  onClose()
                }}
                className={rowCls(active)}
              >
                <div className="w-9 h-9 rounded-full flex items-center justify-center bg-eleball-primary-light shrink-0">
                  <Bot className="w-5 h-5 text-eleball-primary" />
                </div>
                <div className="flex-1 min-w-0">
                  <div className="text-sm font-medium text-eleball-text truncate">{a.name}</div>
                  <div className="text-xs text-eleball-text-tertiary truncate">{(a.items || []).length} 个秘技</div>
                </div>
                {active && <Check className="w-4 h-4 text-eleball-primary shrink-0" />}
              </button>
            )
          })}

          {assistants.length === 0 && (
            <p className="text-xs text-eleball-text-secondary text-center py-4">
              还没有助手，点下方「管理助手」去组合秘技
            </p>
          )}
        </div>

        <Link
          to="/agents?tab=assistants"
          onClick={onClose}
          className="mt-4 w-full flex items-center justify-center gap-2 py-2.5 text-sm font-medium text-eleball-primary bg-eleball-primary-light/50 rounded-xl hover:bg-eleball-primary-light"
        >
          <SettingsIcon className="w-4 h-4" />
          管理助手
        </Link>
      </div>
    </div>
  )
}
