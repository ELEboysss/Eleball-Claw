import { Image as ImageIcon, Film, XCircle, Trash2 } from 'lucide-react'
import { formatCost, isTerminal, STATUS_COLORS, STATUS_LABELS } from '../../utils/visualTasks'

export default function VisualTaskCard({ task, selected, onSelect, onCancel, onDelete }) {
  const Icon = task.media_type === 'video' ? Film : ImageIcon
  const terminal = isTerminal(task.status)

  return (
    <div
      onClick={() => onSelect?.(task)}
      className={`cursor-pointer rounded-lg border p-3 transition-colors ${
        selected ? 'border-[#6750A4] bg-[#6750A4]/20' : 'border-[#2e2e45] bg-[#252538]/50 hover:bg-[#252538]'
      }`}
    >
      <div className="flex items-start gap-3">
        <div className={`mt-0.5 rounded-md p-1.5 ${selected ? 'bg-[#6750A4] text-white' : 'bg-[#6750A4]/20 text-[#b8a5ff]'}`}>
          <Icon className="w-4 h-4" />
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-sm font-medium text-[#e8e8f0] truncate">{task.prompt || '未填写提示词'}</p>
          <p className="mt-1 text-xs text-[#a0a0b8]">
            {task.model} · {formatCost(task.cost, task.currency)}
          </p>
          <div className="mt-2 flex items-center justify-between">
            <span className={`text-xs font-medium ${STATUS_COLORS[task.status] || 'text-[#a0a0b8]'}`}>
              {STATUS_LABELS[task.status] || task.status}
            </span>
            <div className="flex items-center gap-3">
              {!terminal && onCancel && (
                <button
                  onClick={(e) => {
                    e.stopPropagation()
                    onCancel(task.id)
                  }}
                  className="flex items-center gap-1 text-xs text-[#a0a0b8] hover:text-[#ff7b7b]"
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
                  className="flex items-center gap-1 text-xs text-[#a0a0b8] hover:text-[#ff7b7b]"
                >
                  <Trash2 className="w-3 h-3" />
                  删除
                </button>
              )}
            </div>
          </div>
          {task.error_message && (
            <p className="mt-2 text-xs text-[#ff7b7b] line-clamp-2">{task.error_message}</p>
          )}
        </div>
      </div>
    </div>
  )
}
