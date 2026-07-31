import { Image as ImageIcon, Film, XCircle, Trash2 } from 'lucide-react'
import { formatCost, isTerminal, STATUS_COLORS, STATUS_LABELS } from '../../utils/visualTasks'

export default function VisualTaskCard({ task, selected, onSelect, onCancel, onDelete }) {
  const Icon = task.media_type === 'video' ? Film : ImageIcon
  const terminal = isTerminal(task.status)

  return (
    <div
      onClick={() => onSelect?.(task)}
      className={`cursor-pointer rounded-lg border p-3 transition-colors ${
        selected ? 'border-eleball-primary bg-eleball-primary/20' : 'border-eleball-vs-border-variant bg-eleball-vs-surface-variant/50 hover:bg-eleball-vs-surface-variant'
      }`}
    >
      <div className="flex items-start gap-3">
        <div className={`mt-0.5 rounded-md p-1.5 ${selected ? 'bg-eleball-primary text-white' : 'bg-eleball-primary/20 text-eleball-vs-accent'}`}>
          <Icon className="w-4 h-4" />
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium text-eleball-vs-text truncate">{task.prompt || '未填写提示词'}</p>
          <p className="mt-1 text-xs text-eleball-vs-text-muted">
            {task.model} · {formatCost(task.cost, task.currency)}
          </p>
          <div className="mt-2 flex items-center justify-between">
            <span className={`text-xs font-medium ${STATUS_COLORS[task.status] || 'text-eleball-vs-text-muted'}`}>
              {STATUS_LABELS[task.status] || task.status}
            </span>
            <div className="flex items-center gap-3">
              {!terminal && onCancel && (
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    onCancel(task.id)
                  }}
                  className="flex items-center gap-1 text-xs text-eleball-vs-text-muted hover:text-eleball-vs-error"
                >
                  <XCircle className="w-3 h-3" />
                  取消
                </button>
              )}
              {terminal && onDelete && (
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    onDelete(task.id)
                  }}
                  className="flex items-center gap-1 text-xs text-eleball-vs-text-muted hover:text-eleball-vs-error"
                >
                  <Trash2 className="w-3 h-3" />
                  删除
                </button>
              )}
            </div>
          </div>
          {task.error_message && (
            <p className="mt-2 text-xs text-eleball-vs-error line-clamp-2">{task.error_message}</p>
          )}
        </div>
      </div>
    </div>
  )
}
