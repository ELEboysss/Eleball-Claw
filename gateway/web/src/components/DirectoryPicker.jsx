import { useState, useEffect, useCallback } from 'react'
import { ChevronUp, Folder, FolderOpen, Check, X, Loader2 } from 'lucide-react'
import { cwdApi } from '../api/client'

// AR-11 DirectoryPicker：claw 本地工作目录选择弹窗（消费 AR-06 /cwd/browse + /cwd/validate）。
// 浏览目录树选根，或直接输入路径；确认时调 validate 返回 EvalSymlinks 后的绝对 cwd。
// props: { open, onClose, onSelect }  onSelect(resolvedCwd) 确认回调。
export default function DirectoryPicker({ open, onClose, onSelect }) {
  const [path, setPath] = useState('')        // 当前输入/浏览路径
  const [entries, setEntries] = useState([])  // 当前目录子条目（仅目录）
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(async (p) => {
    setLoading(true)
    setError('')
    try {
      const data = await cwdApi.browse(p)
      setPath(data.path)
      setEntries((data.entries || []).filter((e) => e.is_dir))
    } catch (e) {
      setError(e.message || '读取目录失败')
      setEntries([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (open) load('')
  }, [open, load])

  // ESC 关闭
  useEffect(() => {
    if (!open) return
    const onKey = (e) => { if (e.key === 'Escape') onClose?.() }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onClose])

  if (!open) return null

  const goUp = () => {
    const parent = path.replace(/[\\/][^\\/]+$/, '')
    load(parent || (path.startsWith('/') ? '/' : path))
  }

  const confirm = async () => {
    if (!path) return
    setBusy(true)
    setError('')
    try {
      const data = await cwdApi.validate(path)
      onSelect?.(data.cwd)
      onClose?.()
    } catch (e) {
      setError(e.message || '校验失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[70] flex items-center justify-center bg-black/40" role="dialog" aria-modal="true" aria-labelledby="dpp-title">
      <div className="w-full max-w-lg mx-4 bg-white rounded-2xl shadow-xl border border-eleball-outline overflow-hidden flex flex-col max-h-[80vh]">
        <div className="flex items-center justify-between px-4 py-3 border-b border-eleball-outline-variant">
          <h2 id="dpp-title" className="text-sm font-semibold text-eleball-text">选择工作目录</h2>
          <button type="button" onClick={onClose} aria-label="关闭" className="p-1 rounded-lg text-eleball-text-secondary hover:bg-gray-100">
            <X className="w-4 h-4" />
          </button>
        </div>

        {/* 路径输入 + 上级 */}
        <div className="flex items-center gap-2 px-3 py-2 border-b border-eleball-outline-variant">
          <button type="button" onClick={goUp} aria-label="上一级" title="上一级"
            className="p-1.5 rounded-lg text-eleball-text-secondary hover:bg-gray-100 border border-eleball-outline">
            <ChevronUp className="w-4 h-4" />
          </button>
          <input
            value={path}
            onChange={(e) => setPath(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') load(path) }}
            placeholder="输入或浏览目录路径"
            className="flex-1 min-w-0 text-xs px-2 py-1.5 rounded-lg border border-eleball-outline focus:border-eleball-primary focus:outline-none font-mono"
          />
          <button type="button" onClick={() => load(path)}
            className="px-2.5 py-1.5 text-xs rounded-lg bg-eleball-surface border border-eleball-outline hover:bg-eleball-surface-variant">
            进入
          </button>
        </div>

        {/* 目录列表 */}
        <div className="flex-1 overflow-y-auto min-h-[200px] py-1">
          {loading && (
            <div className="flex items-center justify-center py-8 text-eleball-text-secondary text-xs">
              <Loader2 className="w-4 h-4 animate-spin mr-2" /> 加载中…
            </div>
          )}
          {!loading && entries.length === 0 && !error && (
            <div className="py-8 text-center text-xs text-eleball-text-secondary">无子目录</div>
          )}
          {!loading && entries.map((e) => (
            <button
              key={e.name}
              type="button"
              onClick={() => load(path ? `${path.replace(/[\\/]$/, '')}/${e.name}` : e.name)}
              className="w-full flex items-center gap-2 px-4 py-1.5 text-left text-xs text-eleball-text hover:bg-gray-50"
              title={e.name}
            >
              <Folder className="w-4 h-4 text-eleball-primary flex-shrink-0" />
              <span className="truncate">{e.name}</span>
            </button>
          ))}
          {error && (
            <div className="px-4 py-3 text-xs text-eleball-error">{error}</div>
          )}
        </div>

        {/* 底部操作 */}
        <div className="flex items-center justify-end gap-2 px-4 py-3 border-t border-eleball-outline-variant bg-eleball-surface">
          <button type="button" onClick={onClose}
            className="px-3 py-1.5 text-xs rounded-lg border border-eleball-outline hover:bg-gray-50">
            取消
          </button>
          <button type="button" onClick={confirm} disabled={busy || !path}
            className="inline-flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg bg-eleball-primary text-white hover:opacity-90 disabled:opacity-50">
            {busy ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Check className="w-3.5 h-3.5" />}
            选定此目录
          </button>
        </div>
      </div>
    </div>
  )
}
