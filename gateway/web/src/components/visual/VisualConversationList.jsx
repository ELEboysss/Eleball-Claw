import { Plus, Trash2, Image as ImageIcon, Film } from 'lucide-react'

export default function VisualConversationList({ conversations, selectedId, onSelect, onCreate, onDelete, mediaType }) {
  return (
    <div className="flex h-full flex-col rounded-xl border border-eleball-vs-border bg-eleball-vs-surface p-3">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-eleball-vs-text-muted">创作会话</h3>
        <button
          onClick={onCreate}
          className="flex items-center gap-1 rounded-md bg-eleball-primary px-2 py-1 text-xs font-medium text-white hover:bg-eleball-vs-primary-hover transition-colors"
        >
          <Plus className="w-3 h-3" />
          新建
        </button>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto space-y-2 pr-1">
        {conversations.length === 0 && (
          <p className="text-xs text-eleball-vs-text-dim text-center py-4">暂无会话，点击新建开始创作</p>
        )}
        {conversations.map((conv) => {
          const Icon = mediaType === 'video' ? Film : ImageIcon
          const selected = conv.id === selectedId
          return (
            <div
              key={conv.id}
              onClick={() => onSelect(conv.id)}
              className={`group flex cursor-pointer items-center gap-2 rounded-lg border px-2.5 py-2 transition-colors ${
                selected
                  ? 'border-eleball-primary bg-eleball-primary/20'
                  : 'border-eleball-vs-border-variant bg-eleball-vs-surface-variant/50 hover:bg-eleball-vs-surface-variant'
              }`}
            >
              <Icon className={`w-4 h-4 flex-shrink-0 ${selected ? 'text-eleball-vs-accent' : 'text-eleball-vs-text-muted'}`} />
              <div className="min-w-0 flex-1">
                <p className={`text-sm truncate ${selected ? 'text-eleball-vs-text font-medium' : 'text-eleball-vs-text'}`}>{conv.title}</p>
                <p className="text-xs text-eleball-vs-text-dim">
                  {new Date(conv.updated_at).toLocaleDateString()}
                </p>
              </div>
              <button
                onClick={(e) => {
                  e.stopPropagation()
                  onDelete(conv.id)
                }}
                className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100 p-1 rounded text-eleball-vs-text-muted hover:text-eleball-vs-error transition-opacity"
                title="删除会话"
              >
                <Trash2 className="w-3.5 h-3.5" />
              </button>
            </div>
          )
        })}
      </div>
    </div>
  )
}
