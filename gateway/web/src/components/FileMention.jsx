import { useEffect, useRef, useState } from 'react'
import { FileText, Folder, Loader2 } from 'lucide-react'
import { slashApi } from '../api/client'
import { useDebounce } from '../hooks/useDebounce'

/**
 * @ 文件路径 fuzzy 补全面板（C5）
 * @param {Object} props
 * @param {string} props.query @ 后的关键字
 * @param {string} props.cwd 当前工作目录
 * @param {number} props.selectedIndex 选中索引
 * @param {function} props.onSelect 选中回调 (path)
 * @param {function} props.onClose 关闭回调
 */
export default function FileMention({ query, cwd, selectedIndex, onSelect, onClose }) {
  const [files, setFiles] = useState([])
  const [loading, setLoading] = useState(false)
  const containerRef = useRef(null)
  const itemRefs = useRef([])
  const debouncedQuery = useDebounce(query, 150)

  useEffect(() => {
    let cancelled = false
    async function fetchFiles() {
      if (!debouncedQuery) {
        setFiles([])
        return
      }
      setLoading(true)
      try {
        const data = await slashApi.fuzzyFiles(debouncedQuery, cwd || '', 20)
        if (cancelled) return
        setFiles(data?.files || [])
      } catch (err) {
        if (!cancelled) setFiles([])
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    fetchFiles()
    return () => { cancelled = true }
  }, [debouncedQuery, cwd])

  useEffect(() => {
    const el = itemRefs.current[selectedIndex]
    if (el) el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [selectedIndex, files])

  useEffect(() => {
    function onClick(e) {
      if (containerRef.current && !containerRef.current.contains(e.target)) onClose()
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

  if (loading && files.length === 0) {
    return (
      <div
        ref={containerRef}
        className="absolute left-0 right-0 bottom-full mb-2 bg-white rounded-xl border border-eleball-outline shadow-lg p-3 text-sm text-eleball-text-secondary z-50 flex items-center gap-2"
      >
        <Loader2 className="w-4 h-4 animate-spin" />
        搜索文件中…
      </div>
    )
  }

  if (!loading && files.length === 0 && debouncedQuery) {
    return (
      <div
        ref={containerRef}
        className="absolute left-0 right-0 bottom-full mb-2 bg-white rounded-xl border border-eleball-outline shadow-lg p-3 text-sm text-eleball-text-secondary z-50"
      >
        未找到匹配文件
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      className="absolute left-0 right-0 bottom-full mb-2 bg-white rounded-xl border border-eleball-outline shadow-lg overflow-hidden z-50 flex flex-col max-h-[min(360px,50vh)]"
    >
      <div className="flex-1 overflow-y-auto py-1">
        {files.map((file, idx) => {
          const active = idx === selectedIndex
          return (
            <button
              key={file.path}
              ref={(el) => { itemRefs.current[idx] = el }}
              type="button"
              onClick={() => onSelect(file.path)}
              className={[
                'w-full text-left px-3 py-2 flex items-center gap-2 text-sm transition-colors',
                active ? 'bg-eleball-primary-light text-eleball-primary' : 'hover:bg-eleball-surface-variant text-eleball-text'
              ].join(' ')}
            >
              {file.type === 'dir' ? (
                <Folder className="w-4 h-4 text-amber-500 flex-shrink-0" />
              ) : (
                <FileText className="w-4 h-4 text-eleball-primary flex-shrink-0" />
              )}
              <span className="truncate">{file.path}</span>
            </button>
          )
        })}
      </div>
      <div className="px-3 py-1.5 border-t border-eleball-outline-variant bg-eleball-surface text-[10px] text-eleball-text-secondary flex items-center gap-3">
        <span><kbd className="font-sans bg-white px-1 rounded border">↑↓</kbd> 选择</span>
        <span><kbd className="font-sans bg-white px-1 rounded border">Enter</kbd> 插入</span>
        <span><kbd className="font-sans bg-white px-1 rounded border">Esc</kbd> 关闭</span>
      </div>
    </div>
  )
}
