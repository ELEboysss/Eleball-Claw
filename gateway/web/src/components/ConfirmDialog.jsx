import { useEffect, useRef } from 'react'
import { AlertTriangle } from 'lucide-react'

/**
 * ConfirmDialog 替代原生 confirm() 的可访问确认弹窗（AR-05-O5）。
 * - focus trap：打开时聚焦取消按钮，Tab 不离开弹窗，关闭后恢复焦点到触发器
 * - Escape 关闭（视为取消）
 * - role="dialog" aria-modal="true" aria-labelledby
 *
 * Props:
 * - open: 是否显示
 * - title: 标题
 * - message: 正文
 * - confirmText / cancelText: 按钮文案
 * - danger: 是否危险操作（红色确认按钮）
 * - onConfirm: 确认回调
 * - onCancel: 取消回调（Escape/遮罩点击/取消按钮）
 */
export default function ConfirmDialog({
  open,
  title = '确认操作',
  message,
  confirmText = '确认',
  cancelText = '取消',
  danger = true,
  onConfirm,
  onCancel
}) {
  const confirmRef = useRef(null)
  const dialogRef = useRef(null)
  const triggerRef = useRef(null) // 记录打开时的焦点元素，关闭后恢复

  useEffect(() => {
    if (!open) return
    // 记录触发器并聚焦确认按钮
    triggerRef.current = document.activeElement
    const t = setTimeout(() => confirmRef.current?.focus(), 0)
    return () => clearTimeout(t)
  }, [open])

  // 关闭后恢复焦点
  useEffect(() => {
    if (open) return
    if (triggerRef.current && typeof triggerRef.current.focus === 'function') {
      triggerRef.current.focus()
    }
  }, [open])

  if (!open) return null

  const handleKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      onCancel?.()
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
        aria-labelledby="confirm-dialog-title"
        className="card w-full max-w-sm"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <div className="flex items-start gap-3 p-5">
          {danger && (
            <AlertTriangle className="w-6 h-6 text-eleball-error flex-shrink-0 mt-0.5" aria-hidden="true" />
          )}
          <div className="flex-1">
            <h3 id="confirm-dialog-title" className="text-lg font-bold text-eleball-text mb-1">
              {title}
            </h3>
            <p className="text-sm text-eleball-text-secondary">{message}</p>
          </div>
        </div>
        <div className="flex gap-3 px-5 pb-5">
          <button
            onClick={onCancel}
            className="flex-1 btn-ghost"
          >
            {cancelText}
          </button>
          <button
            ref={confirmRef}
            onClick={onConfirm}
            className={`flex-1 px-4 py-2 rounded-lg text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-eleball-primary ${
              danger ? 'bg-eleball-error hover:brightness-105' : 'btn-primary'
            }`}
          >
            {confirmText}
          </button>
        </div>
      </div>
    </div>
  )
}
