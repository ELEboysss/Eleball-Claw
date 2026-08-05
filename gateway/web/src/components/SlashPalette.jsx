import { useEffect, useMemo, useRef, useState } from 'react'
import { Command, Wrench, FileText, Terminal, Trash2, Bot, Brain, FolderInput } from 'lucide-react'

const ICON_SIZE = 'w-4 h-4'

const CATEGORY_ICONS = {
  builtin: <Terminal className={ICON_SIZE} />,
  skills: <Wrench className={ICON_SIZE} />,
  templates: <FileText className={ICON_SIZE} />
}

const COMMAND_ICONS = {
  '/clear': <Trash2 className={ICON_SIZE} />,
  '/compact': <Command className={ICON_SIZE} />,
  '/plan': <FolderInput className={ICON_SIZE} />,
  '/model': <Bot className={ICON_SIZE} />,
  '/memory': <Brain className={ICON_SIZE} />
}

function normalizeIcon(icon) {
  if (!icon) return null
  // 后端可能返回 emoji 字符串或图片 URL
  if (icon.startsWith('http')) {
    return <img src={icon} alt="" className="w-4 h-4 rounded object-cover" />
  }
  return <span className="text-base leading-none">{icon}</span>
}

/**
 * Slash 命令面板（C5）
 * @param {Object} props
 * @param {Array} props.categories 分组命令列表
 * @param {string} props.query 当前 `/` 后的关键字
 * @param {number} props.selectedIndex 全局选中索引
 * @param {function} props.onSelect 选择命令回调 (command, argsText)
 * @param {function} props.onClose 关闭面板回调
 */
export default function SlashPalette({ categories, query, selectedIndex, onSelect, onClose }) {
  const containerRef = useRef(null)
  const itemRefs = useRef([])
  const [flattened, setFlattened] = useState([])

  // 把所有命令打平，便于键盘导航
  useEffect(() => {
    const items = []
    for (const cat of categories || []) {
      for (const cmd of cat.commands || []) {
        items.push({ ...cmd, categoryLabel: cat.label, categoryName: cat.name })
      }
    }
    setFlattened(items)
    itemRefs.current = items.map(() => null)
  }, [categories])

  // 滚动到选中项
  useEffect(() => {
    const el = itemRefs.current[selectedIndex]
    if (el) {
      el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
    }
  }, [selectedIndex])

  // 点击外部关闭
  useEffect(() => {
    function onClick(e) {
      if (containerRef.current && !containerRef.current.contains(e.target)) {
        onClose()
      }
    }
    function onKey(e) {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('mousedown', onClick)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onClick)
      document.removeEventListener('keydown', onKey)
    }
  }, [onClose])

  const selectedCommand = flattened[selectedIndex]

  const handleSelect = (cmd) => {
    // 提取 query 中命令名后的剩余文本作为参数
    const name = cmd.name
    const nameLower = name.toLowerCase()
    const q = query.toLowerCase()
    let argsText = ''
    if (q.startsWith(nameLower)) {
      argsText = query.slice(nameLower.length).trimStart()
    } else {
      argsText = query.trimStart()
    }
    onSelect(cmd, argsText)
  }

  if (categories.length === 0) {
    return (
      <div
        ref={containerRef}
        className="absolute left-0 right-0 bottom-full mb-2 bg-white rounded-xl border border-eleball-outline shadow-lg p-3 text-sm text-eleball-text-secondary z-50"
      >
        暂无可用命令
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      className="absolute left-0 right-0 bottom-full mb-2 bg-white rounded-xl border border-eleball-outline shadow-lg overflow-hidden z-50 flex flex-col max-h-[min(420px,60vh)]"
    >
      <div className="flex-1 overflow-y-auto py-2">
        {categories.map((cat) => {
          if (!cat.commands || cat.commands.length === 0) return null
          return (
            <div key={cat.name} className="mb-1 last:mb-0">
              <div className="px-3 py-1.5 text-xs font-medium text-eleball-text-secondary flex items-center gap-1.5 sticky top-0 bg-white z-10">
                {CATEGORY_ICONS[cat.name] || <Command className={ICON_SIZE} />}
                {cat.label}
              </div>
              {cat.commands.map((cmd) => {
                const globalIdx = flattened.findIndex((c) => c.name === cmd.name && c.categoryName === cat.name)
                const active = selectedCommand?.name === cmd.name && selectedCommand?.categoryName === cat.name
                const icon = cmd.icon ? normalizeIcon(cmd.icon) : (COMMAND_ICONS[cmd.name] || <Command className={ICON_SIZE} />)
                return (
                  <button
                    key={`${cat.name}-${cmd.name}`}
                    ref={(el) => { itemRefs.current[globalIdx] = el }}
                    type="button"
                    onClick={() => handleSelect(cmd)}
                    className={[
                      'w-full text-left px-3 py-2 flex items-start gap-2.5 transition-colors',
                      active ? 'bg-eleball-surface-variant text-eleball-primary' : 'hover:bg-eleball-surface-variant/50 text-eleball-text'
                    ].join(' ')}
                  >
                    <span className="mt-0.5 text-eleball-text-secondary">{icon}</span>
                    <div className="flex-1 min-w-0">
                      <div className="text-sm font-medium truncate">
                        {cmd.name}
                        {cmd.arguments_hint && (
                          <span className="ml-1.5 text-xs font-normal text-eleball-text-secondary">{cmd.arguments_hint}</span>
                        )}
                      </div>
                      <div className="text-xs text-eleball-text-secondary truncate">{cmd.description}</div>
                    </div>
                  </button>
                )
              })}
            </div>
          )
        })}
      </div>
      <div className="px-3 py-1.5 border-t border-eleball-outline-variant bg-eleball-surface text-[10px] text-eleball-text-secondary flex items-center gap-3">
        <span><kbd className="font-mono bg-white px-1 rounded border">↑↓</kbd> 选择</span>
        <span><kbd className="font-mono bg-white px-1 rounded border">Enter</kbd> 应用</span>
        <span><kbd className="font-mono bg-white px-1 rounded border">Esc</kbd> 关闭</span>
      </div>
    </div>
  )
}
