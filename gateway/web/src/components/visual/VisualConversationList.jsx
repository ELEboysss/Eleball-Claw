import { Plus, Trash2, Image as ImageIcon, Film } from 'lucide-react'

export default function VisualConversationList({ conversations, selectedId, onSelect, onCreate, onDelete, mediaType }) {
  return (
    <div className="flex h-full flex-col rounded-xl border border-[#26263a] bg-[#1c1c2b] p-3">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold text-[#a0a0b8]">创作会话</h3>
        <button
          onClick={onCreate}
          className="flex items-center gap-1 rounded-md bg-[#6750A4] px-2 py-1 text-xs font-medium text-white hover:bg-[#4a3b7a] transition-colors"
        >
          <Plus className="w-3 h-3" />
          新建
        </button>
      </div>

      <div className="flex-1 min-h-0 overflow-y-auto space-y-2 pr-1">
        {conversations.length === 0 && (
          <p className="text-xs text-[#6e6e8a] text-center py-4">暂无会话，点击新建开始创作</p>
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
                  ? 'border-[#6750A4] bg-[#6750A4]/20'
                  : 'border-[#2e2e45] bg-[#252538]/50 hover:bg-[#252538]'
              }`}
            >
              <Icon className={`w-4 h-4 flex-shrink-0 ${selected ? 'text-[#b8a5ff]' : 'text-[#a0a0b8]'}`} />
              <div className="min-w-0 flex-1">
                <p className={`text-sm truncate ${selected ? 'text-[#e8e8f0] font-medium' : 'text-[#e8e8f0]'}`}>{conv.title}</p>
                <p className="text-xs text-[#6e6e8a]">
                  {new Date(conv.updated_at).toLocaleDateString()}
                </p>
              </div>
              <button
                onClick={(e) => {
                  e.stopPropagation()
                  onDelete(conv.id)
                }}
                className="opacity-0 group-hover:opacity-100 focus-visible:opacity-100 p-1 rounded text-[#a0a0b8] hover:text-[#ff7b7b] transition-opacity"
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
