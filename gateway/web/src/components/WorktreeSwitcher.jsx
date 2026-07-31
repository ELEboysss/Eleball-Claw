import { useState, useEffect, useRef, useCallback } from 'react'
import { GitBranch, Plus, Trash2, Check, ChevronDown, Loader2, X } from 'lucide-react'
import { worktreeApi } from '../api/client'

// AR-17 O16 WorktreeSwitcher：claw 本地 worktree 列出/切换/创建/删除。
//
// 仿 pi-web SessionSidebar 的 worktree 切换器，消费 /v1/claw-console/worktrees：
//   - 仅当 cwd 为 git 仓库顶层检出时渲染（isGit && isTopLevel），否则返回 null
//   - 切换 worktree = 调 onCwdChange(wt.path)，由父组件更新 cwd（FileExplorer/Git 状态跟随）
//   - 创建 worktree 在 <repoRoot>-worktrees/<branch>，创建后自动切换到新检出
//   - 删除 worktree 保留分支；dirty 检出返回 dirty=true 触发 force 二次确认
//
// 样式用 eleball-* token（浅色主题，对齐 DirectoryPicker/FileExplorer）；
// 动画仅 transition-colors，由 index.css 的 prefers-reduced-motion 全局降级。
export default function WorktreeSwitcher({ cwd, onCwdChange }) {
  const [state, setState] = useState(null) // { projectRoot, isGit, isTopLevel, worktrees }
  const [loading, setLoading] = useState(false)
  const [open, setOpen] = useState(false)
  const [newOpen, setNewOpen] = useState(false)
  const [newBranch, setNewBranch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [confirmRemove, setConfirmRemove] = useState(null) // 待 force 确认的 worktree path
  const wrapRef = useRef(null)
  const newInputRef = useRef(null)

  // 拉取 worktree 列表（cwd 变化时）
  const refresh = useCallback(async () => {
    if (!cwd) { setState(null); return }
    setLoading(true)
    setError('')
    try {
      const data = await worktreeApi.list(cwd)
      setState(data)
    } catch {
      setState(null)
    } finally {
      setLoading(false)
    }
  }, [cwd])

  useEffect(() => { refresh() }, [refresh])

  // 点外部 / ESC 关闭下拉
  useEffect(() => {
    if (!open) return
    const onDown = (e) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) {
        setOpen(false); setNewOpen(false); setNewBranch(''); setError(''); setConfirmRemove(null)
      }
    }
    const onKey = (e) => { if (e.key === 'Escape') { setOpen(false); setNewOpen(false); setConfirmRemove(null) } }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => { document.removeEventListener('mousedown', onDown); document.removeEventListener('keydown', onKey) }
  }, [open])

  // 仅 git 仓库顶层检出才渲染（子目录与非 git 目录隐藏切换器）
  const show = state?.isGit && state.isTopLevel
  if (!cwd || (!show && !loading)) return null

  const worktrees = state?.worktrees || []
  const currentWt =
    worktrees.find((w) => w.path === cwd) || worktrees.find((w) => w.isMain) || null
  const label = currentWt
    ? (currentWt.branch || currentWt.path.split(/[\\/]/).pop())
    : '…'

  const handleCreate = async () => {
    const branch = newBranch.trim()
    if (!branch || busy || !state) return
    setBusy(true); setError('')
    try {
      const data = await worktreeApi.create(state.projectRoot, branch)
      setNewOpen(false); setNewBranch(''); setOpen(false)
      onCwdChange?.(data.path)
      refresh()
    } catch (e) {
      setError(e.message || '创建失败')
    } finally {
      setBusy(false)
    }
  }

  const handleRemove = async (path, force) => {
    if (busy || !state) return
    setBusy(true); setError('')
    try {
      const data = await worktreeApi.remove(state.projectRoot, path, force)
      if (data?.dirty && !force) {
        setConfirmRemove(path) // 有未提交改动，要求 force 二次确认
        return
      }
      setConfirmRemove(null)
      // 删除的是当前 worktree -> 回退到主检出
      if (path === cwd) {
        const main = worktrees.find((w) => w.isMain)
        if (main) onCwdChange?.(main.path)
      }
      refresh()
    } catch (e) {
      setError(e.message || '删除失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div ref={wrapRef} className="relative">
      {/* 触发按钮：当前 worktree 分支 + 计数 + 展开箭头 */}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-label={currentWt ? `切换 worktree：${currentWt.path}` : '切换 worktree'}
        title={currentWt?.path || '切换 worktree'}
        className="w-full flex items-center gap-1.5 h-7 px-2.5 text-xs rounded-lg bg-eleball-surface hover:bg-eleball-surface-variant border border-eleball-outline-variant text-eleball-text-secondary transition-colors text-left"
      >
        <GitBranch className={`w-3.5 h-3.5 flex-shrink-0 ${currentWt && !currentWt.isMain ? 'text-eleball-primary' : 'text-eleball-text-tertiary'}`} />
        <span className="flex-1 min-w-0 truncate font-mono">{label}</span>
        {currentWt?.isMain && <span className="flex-shrink-0 text-[10px] text-eleball-text-tertiary">main</span>}
        {worktrees.length > 1 && (
          <span className="flex-shrink-0 text-[10px] text-eleball-text-tertiary">{worktrees.length}</span>
        )}
        <ChevronDown className={`w-3 h-3 flex-shrink-0 text-eleball-text-tertiary transition-transform ${open ? 'rotate-180' : ''}`} />
      </button>

      {open && (
        <div
          className="absolute top-full left-0 right-0 mt-1 z-[60] bg-white rounded-lg shadow-lg border border-eleball-outline-variant overflow-hidden"
          role="listbox"
        >
          <div className="max-h-[40vh] overflow-y-auto">
            {worktrees.map((wt) => {
              const isCurrent = wt.path === cwd || (wt.isMain && !worktrees.some((w) => w.path === cwd))
              // force 二次确认行
              if (confirmRemove === wt.path) {
                return (
                  <div key={wt.path} className="flex items-center gap-2 px-2.5 py-1.5 border-b border-eleball-outline-variant bg-red-50">
                    <span className="flex-1 min-w-0 text-[11px] text-eleball-text truncate">有未提交改动，强制删除检出？</span>
                    <button type="button" onClick={() => handleRemove(wt.path, true)} disabled={busy}
                      className="px-2 py-0.5 text-[11px] rounded bg-eleball-error text-white font-medium disabled:opacity-50">强制</button>
                    <button type="button" onClick={() => setConfirmRemove(null)}
                      className="px-2 py-0.5 text-[11px] rounded border border-eleball-outline text-eleball-text-secondary">取消</button>
                  </div>
                )
              }
              return (
                <div key={wt.path} className="flex items-center border-b border-eleball-outline-variant last:border-b-0">
                  <button
                    type="button"
                    onClick={() => { onCwdChange?.(wt.path); setOpen(false); setError('') }}
                    title={wt.path}
                    aria-selected={isCurrent}
                    role="option"
                    className={`flex-1 min-w-0 flex items-center gap-2 px-2.5 py-1.5 text-left text-xs ${isCurrent ? 'text-eleball-text font-medium bg-eleball-primary-light/30' : 'text-eleball-text-secondary hover:bg-gray-50'}`}
                  >
                    {isCurrent ? <Check className="w-3.5 h-3.5 flex-shrink-0 text-eleball-primary" /> : <span className="w-3.5 flex-shrink-0" />}
                    <span className="flex-1 min-w-0 truncate font-mono">{wt.branch || wt.path.split(/[\\/]/).pop()}</span>
                    {wt.isMain && <span className="flex-shrink-0 text-[10px] text-eleball-text-tertiary">main</span>}
                  </button>
                  {!wt.isMain && (
                    <button
                      type="button"
                      onClick={() => handleRemove(wt.path, false)}
                      disabled={busy}
                      aria-label={`删除 worktree ${wt.path}`}
                      title={`删除 worktree 检出（保留分支）${wt.path}`}
                      className="flex-shrink-0 w-7 h-7 mr-1 flex items-center justify-center text-eleball-text-tertiary hover:text-eleball-error hover:bg-red-50 rounded transition-colors disabled:opacity-50"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  )}
                </div>
              )
            })}
          </div>

          {/* 新建 worktree */}
          {!newOpen ? (
            <button
              type="button"
              onClick={() => { setNewOpen(true); setError(''); setTimeout(() => newInputRef.current?.focus(), 0) }}
              className="w-full flex items-center gap-2 px-2.5 py-1.5 text-xs text-eleball-text-secondary hover:bg-gray-50 transition-colors"
            >
              <Plus className="w-3.5 h-3.5 flex-shrink-0" />
              <span>新建 worktree…</span>
            </button>
          ) : (
            <div className="px-2 py-1.5 border-t border-eleball-outline-variant">
              <input
                ref={newInputRef}
                value={newBranch}
                onChange={(e) => { setNewBranch(e.target.value); setError('') }}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') { e.preventDefault(); handleCreate() }
                  if (e.key === 'Escape') { setNewOpen(false); setNewBranch(''); setError('') }
                }}
                placeholder="分支名"
                className="w-full text-xs px-2 py-1 rounded border border-eleball-primary focus:outline-none font-mono"
              />
              <div className="flex gap-1.5 mt-1.5">
                <button type="button" onClick={handleCreate} disabled={busy || !newBranch.trim()}
                  className="flex-1 inline-flex items-center justify-center gap-1 py-1 text-xs rounded bg-eleball-primary text-white font-medium hover:opacity-90 disabled:opacity-50">
                  {busy ? <Loader2 className="w-3 h-3 animate-spin" /> : <Plus className="w-3 h-3" />}
                  创建
                </button>
                <button type="button" onClick={() => { setNewOpen(false); setNewBranch(''); setError('') }}
                  className="flex-1 py-1 text-xs rounded border border-eleball-outline text-eleball-text-secondary hover:bg-gray-50">
                  取消
                </button>
              </div>
            </div>
          )}

          {error && (
            <div className="px-2.5 py-1.5 text-[11px] text-eleball-error break-words border-t border-eleball-outline-variant flex items-start gap-1">
              <X className="w-3 h-3 flex-shrink-0 mt-0.5" />
              <span>{error}</span>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
