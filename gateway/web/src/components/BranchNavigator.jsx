import { useState, useRef, useEffect } from 'react'
import { GitBranch, ChevronDown } from 'lucide-react'

// AR-12 会话分叉分支导航：根据 forkLinks 显示当前对话的父对话（返回主线）与子分叉（列表跳转）。
// forkLinks 由 Chat.jsx 维护（fork 时写入并持久化到 localStorage），结构：
//   { [convId]: { parent: parentConvId, children: [childConvId, ...] } }
// 无父无子时返回 null（不占位）。
export default function BranchNavigator({ currentConversationId, forkLinks, conversations, onNavigate }) {
  const [open, setOpen] = useState(false)
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    const handler = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', handler)
    return () => document.removeEventListener('mousedown', handler)
  }, [open])

  if (!currentConversationId || !forkLinks) return null
  const link = forkLinks[currentConversationId]
  const parent = link?.parent
  const children = link?.children || []
  if (!parent && children.length === 0) return null

  const convTitle = (id) => {
    const c = conversations?.find((x) => x.id === id)
    return c?.title || '未命名对话'
  }

  return (
    <div className="relative flex items-center" ref={ref}>
      {parent && (
        <button
          type="button"
          onClick={() => onNavigate?.(parent)}
          title={`返回父对话：${convTitle(parent)}`}
          aria-label={`返回父对话：${convTitle(parent)}`}
          className="inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs font-medium border border-eleball-outline bg-transparent text-eleball-text-secondary hover:bg-gray-50 hover:text-eleball-text transition-colors"
        >
          <GitBranch className="w-3.5 h-3.5" />
          <span>主线</span>
        </button>
      )}
      {children.length > 0 && (
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-label={`查看分叉（${children.length}）`}
          title={`分叉（${children.length}）`}
          className="ml-1 inline-flex items-center gap-1 px-2 py-1 rounded-full text-xs font-medium border border-eleball-outline bg-transparent text-eleball-text-secondary hover:bg-gray-50 hover:text-eleball-text transition-colors"
        >
          <GitBranch className="w-3.5 h-3.5" />
          <span>分叉({children.length})</span>
          <ChevronDown className="w-3 h-3" />
        </button>
      )}
      {open && children.length > 0 && (
        <div className="absolute top-full left-0 mt-1 z-50 min-w-[180px] max-w-[260px] bg-white border border-eleball-outline rounded-lg shadow-lg py-1 max-h-60 overflow-y-auto">
          {children.map((cid) => (
            <button
              key={cid}
              type="button"
              onClick={() => { setOpen(false); onNavigate?.(cid) }}
              className="w-full text-left px-3 py-1.5 text-xs text-eleball-text hover:bg-gray-50 truncate"
              title={convTitle(cid)}
            >
              {convTitle(cid)}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
