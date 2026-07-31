import { useEffect, useRef, useState } from 'react'

/**
 * PromptDialog 文本输入弹窗（AR-21，FileExplorer 新建目录/重命名消费）。
 * 仿 ConfirmDialog：focus trap、Escape 取消、关闭后恢复焦点、role=dialog aria-modal。
 *
 * Props:
 * - open: 是否显示
 * - title: 标题
 * - label: 输入框标签（可空）
 * - defaultValue: 输入框初值
 * - placeholder: 输入框占位
 * - confirmText / cancelText: 按钮文案
 * - onConfirm(value): 确认回调（空值不触发）
 * - onCancel: 取消回调（Escape/遮罩点击/取消按钮）
 */
export default function PromptDialog({
  open,
  title = '输入',
  label,
  defaultValue = '',
  placeholder = '',
  confirmText = '确认',
  cancelText = '取消',
  onConfirm,
  onCancel
}) {
  const [value, setValue] = useState(defaultValue)
  const inputRef = useRef(null)
  const dialogRef = useRef(null)
  const triggerRef = useRef(null) // 记录打开时的焦点元素，关闭后恢复

  useEffect(() => {
    if (!open) return
    setValue(defaultValue)
    triggerRef.current = document.activeElement
    const t = setTimeout(() => inputRef.current?.focus(), 0)
    return () => clearTimeout(t)
  }, [open, defaultValue])

  // 关闭后恢复焦点
  useEffect(() => {
    if (open) return
    if (triggerRef.current && typeof triggerRef.current.focus === 'function') {
      triggerRef.current.focus()
    }
  }, [open])

  if (!open) return null

  const submit = () => {
    const v = value.trim()
    if (!v) return
    onConfirm?.(v)
  }

  const handleKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel?.()
      return
    }
    if (e.key === 'Enter') {
      e.preventDefault()
      submit()
      return
    }
    // 简易 focus trap：Tab/Shift+Tab 在弹窗内可聚焦元素间循环
    if (e.key === 'Tab') {
      const dialog = dialogRef.current
      if (!dialog) return
      const focusables = dialog.querySelectorAll(
        'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
      )
      if (focusables.length === 0) return
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first.focus()
      }
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4"
      onClick={onCancel}
    >
      <div
        ref={dialogRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby="prompt-dialog-title"
        className="card w-full max-w-sm"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <div className="p-5">
          <h3 id="prompt-dialog-title" className="text-lg font-bold text-eleball-text mb-3">
            {title}
          </h3>
          {label && (
            <label className="block text-xs text-eleball-text-secondary mb-1">{label}</label>
          )}
          <input
            ref={inputRef}
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder={placeholder}
            className="w-full px-3 py-2 rounded-lg border border-eleball-outline text-sm text-eleball-text focus:border-eleball-primary focus:outline-none font-mono"
          />
        </div>
        <div className="flex gap-3 px-5 pb-5">
          <button
            onClick={onCancel}
            className="flex-1 px-4 py-2 rounded-lg border border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-eleball-primary"
          >
            {cancelText}
          </button>
          <button
            onClick={submit}
            disabled={!value.trim()}
            className="flex-1 px-4 py-2 rounded-lg btn-primary text-white disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-eleball-primary"
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  )
}
