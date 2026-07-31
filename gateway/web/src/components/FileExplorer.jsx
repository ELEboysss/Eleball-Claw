import { useState, useEffect, useMemo, useCallback } from 'react'
import { ChevronRight, ChevronDown, Folder, FolderOpen, File as FileIcon, Loader2, RefreshCw, FolderPlus, Pencil, Trash2 } from 'lucide-react'
import { clawFilesApi } from '../api/client'
import PromptDialog from './PromptDialog'
import ConfirmDialog from './ConfirmDialog'

// AR-11 FileExplorer：claw 本地文件树（懒加载展开 + git 状态色标）。
// AR-21：新增文件管理（新建目录、重命名、删除），工具栏 + 节点 hover 操作。
// props:
//   cwd        工作目录绝对路径（沙箱根）
//   onOpenFile(relPath)  点击文件回调
//   gitStatus  { is_repo, entries: [{path,x,y,status}] } 可空
//   refreshKey 变化时重载根（外部触发刷新，如 worktree 切换）
export default function FileExplorer({ cwd, onOpenFile, gitStatus, refreshKey }) {
  const [rootEntries, setRootEntries] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [reloadKey, setReloadKey] = useState(0) // 内部操作后触发刷新
  const [busy, setBusy] = useState(false)
  // prompt: { type: 'mkdir'|'rename', baseRel: string, oldRel?: string, defaultName: string }
  const [prompt, setPrompt] = useState(null)
  // confirm: { relPath: string, isDir: boolean }
  const [confirm, setConfirm] = useState(null)

  // path -> status 映射，供文件色标 O(1) 查找
  const statusMap = useMemo(() => {
    const m = new Map()
    if (gitStatus?.entries) {
      for (const e of gitStatus.entries) m.set(e.path, e.status)
    }
    return m
  }, [gitStatus])

  const loadRoot = useCallback(async () => {
    if (!cwd) return
    setLoading(true)
    setError('')
    try {
      const data = await clawFilesApi.list(cwd, '.')
      setRootEntries(data.entries || [])
    } catch (e) {
      setError(e.message || '读取目录失败')
      setRootEntries([])
    } finally {
      setLoading(false)
    }
  }, [cwd])

  useEffect(() => { loadRoot() }, [loadRoot, refreshKey, reloadKey])

  // 相对路径辅助
  const joinRel = (base, name) => (base ? `${base}/${name}` : name)
  const baseNameOf = (rel) => (rel.includes('/') ? rel.slice(rel.lastIndexOf('/') + 1) : rel)
  const parentRelOf = (rel) => (rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/')) : '')

  // 新建目录（根或某目录下）
  const requestMkdir = (baseRel) => setPrompt({ type: 'mkdir', baseRel, defaultName: '' })
  // 重命名
  const requestRename = (rel) =>
    setPrompt({ type: 'rename', baseRel: parentRelOf(rel), oldRel: rel, defaultName: baseNameOf(rel) })
  // 删除
  const requestDelete = (rel, isDir) => setConfirm({ relPath: rel, isDir })

  const onPromptConfirm = async (name) => {
    if (!prompt || !cwd) return
    setBusy(true)
    setError('')
    try {
      if (prompt.type === 'mkdir') {
        await clawFilesApi.createDir(cwd, joinRel(prompt.baseRel, name))
      } else {
        await clawFilesApi.move(cwd, prompt.oldRel, joinRel(prompt.baseRel, name))
      }
      setPrompt(null)
      setReloadKey((k) => k + 1)
    } catch (e) {
      setError(e.message || '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const onConfirmDelete = async () => {
    if (!confirm || !cwd) return
    setBusy(true)
    setError('')
    try {
      await clawFilesApi.remove(cwd, confirm.relPath)
      setConfirm(null)
      setReloadKey((k) => k + 1)
    } catch (e) {
      setError(e.message || '删除失败')
    } finally {
      setBusy(false)
    }
  }

  if (!cwd) {
    return <div className="p-4 text-xs text-eleball-text-secondary">未设置工作目录</div>
  }

  return (
    <div className="flex flex-col h-full text-xs">
      <div className="flex items-center justify-between px-3 py-2 border-b border-eleball-outline-variant">
        <span className="font-medium text-eleball-text truncate" title={cwd}>文件</span>
        <div className="flex items-center gap-0.5">
          <button type="button" onClick={() => requestMkdir('')} disabled={busy}
            aria-label="新建目录" title="新建目录"
            className="p-1 rounded text-eleball-text-secondary hover:bg-gray-100 disabled:opacity-50">
            <FolderPlus className="w-3.5 h-3.5" />
          </button>
          <button type="button" onClick={loadRoot} disabled={busy}
            aria-label="刷新文件树" title="刷新"
            className="p-1 rounded text-eleball-text-secondary hover:bg-gray-100 disabled:opacity-50">
            <RefreshCw className="w-3.5 h-3.5" />
          </button>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto py-1">
        {loading && (
          <div className="flex items-center px-3 py-4 text-eleball-text-secondary">
            <Loader2 className="w-3.5 h-3.5 animate-spin mr-2" /> 加载中…
          </div>
        )}
        {error && <div className="px-3 py-2 text-eleball-error">{error}</div>}
        {!loading && !error && rootEntries.map((e) => (
          <TreeNode
            key={e.name}
            cwd={cwd}
            relPath={e.name}
            name={e.name}
            isDir={e.is_dir}
            depth={0}
            statusMap={statusMap}
            onOpenFile={onOpenFile}
            onMkdir={requestMkdir}
            onRename={requestRename}
            onDelete={requestDelete}
            busy={busy}
          />
        ))}
      </div>

      {prompt && (
        <PromptDialog
          open
          title={prompt.type === 'mkdir' ? '新建目录' : '重命名'}
          label={prompt.type === 'mkdir' ? '目录名' : '新名称'}
          defaultValue={prompt.defaultName}
          placeholder={prompt.type === 'mkdir' ? '输入目录名' : '输入新名称'}
          confirmText={prompt.type === 'mkdir' ? '创建' : '重命名'}
          onConfirm={onPromptConfirm}
          onCancel={() => setPrompt(null)}
        />
      )}
      {confirm && (
        <ConfirmDialog
          open
          title="确认删除"
          message={`确定删除 ${confirm.isDir ? '目录' : '文件'} “${confirm.relPath}”？${confirm.isDir ? '目录下所有内容将被递归删除。' : ''}`}
          confirmText="删除"
          danger
          onConfirm={onConfirmDelete}
          onCancel={() => setConfirm(null)}
        />
      )}
    </div>
  )
}

// TreeNode 单节点：目录可展开懒加载子项；文件可点击打开。hover 显示操作按钮（AR-21）。
function TreeNode({ cwd, relPath, name, isDir, depth, statusMap, onOpenFile, onMkdir, onRename, onDelete, busy }) {
  const [expanded, setExpanded] = useState(false)
  const [children, setChildren] = useState([])
  const [loadingKids, setLoadingKids] = useState(false)

  const toggle = async () => {
    if (!isDir) {
      onOpenFile?.(relPath)
      return
    }
    const next = !expanded
    setExpanded(next)
    if (next && children.length === 0) {
      setLoadingKids(true)
      try {
        const data = await clawFilesApi.list(cwd, relPath)
        setChildren(data.entries || [])
      } catch {
        setChildren([])
      } finally {
        setLoadingKids(false)
      }
    }
  }

  const status = statusMap.get(relPath)
  const pad = { paddingLeft: `${depth * 12 + 12}px` }
  const stop = (e) => e.stopPropagation()

  return (
    <div>
      <div className="group flex items-center">
        <button
          type="button"
          onClick={toggle}
          style={pad}
          className="flex-1 min-w-0 flex items-center gap-1 py-1 pr-1 text-left text-eleball-text hover:bg-gray-50"
          title={relPath}
        >
          {isDir ? (
            <>
              {expanded ? <ChevronDown className="w-3 h-3 flex-shrink-0" /> : <ChevronRight className="w-3 h-3 flex-shrink-0" />}
              {expanded ? <FolderOpen className="w-3.5 h-3.5 text-eleball-primary flex-shrink-0" /> : <Folder className="w-3.5 h-3.5 text-eleball-primary flex-shrink-0" />}
            </>
          ) : (
            <>
              <span className="w-3 flex-shrink-0" />
              <FileIcon className="w-3.5 h-3.5 text-eleball-text-secondary flex-shrink-0" />
            </>
          )}
          <span className="truncate flex-1 min-w-0">{name}</span>
          {status && <GitDot status={status} />}
        </button>
        {/* AR-21：hover 操作按钮 */}
        <div className="flex items-center pr-2 opacity-0 group-hover:opacity-100 focus-within:opacity-100">
          {isDir && (
            <button type="button" onClick={(e) => { stop(e); onMkdir?.(relPath) }} disabled={busy}
              aria-label="在此目录下新建" title="新建目录"
              className="p-0.5 rounded text-eleball-text-secondary hover:bg-gray-200 disabled:opacity-50">
              <FolderPlus className="w-3 h-3" />
            </button>
          )}
          <button type="button" onClick={(e) => { stop(e); onRename?.(relPath) }} disabled={busy}
            aria-label="重命名" title="重命名"
            className="p-0.5 rounded text-eleball-text-secondary hover:bg-gray-200 disabled:opacity-50">
            <Pencil className="w-3 h-3" />
          </button>
          <button type="button" onClick={(e) => { stop(e); onDelete?.(relPath, isDir) }} disabled={busy}
            aria-label="删除" title="删除"
            className="p-0.5 rounded text-eleball-text-secondary hover:bg-gray-200 hover:text-red-500 disabled:opacity-50">
            <Trash2 className="w-3 h-3" />
          </button>
        </div>
      </div>
      {isDir && expanded && (
        <div>
          {loadingKids && (
            <div style={pad} className="flex items-center py-1 text-eleball-text-secondary">
              <Loader2 className="w-3 h-3 animate-spin mr-1" /> 加载中…
            </div>
          )}
          {!loadingKids && children.map((c) => (
            <TreeNode
              key={c.name}
              cwd={cwd}
              relPath={`${relPath}/${c.name}`}
              name={c.name}
              isDir={c.is_dir}
              depth={depth + 1}
              statusMap={statusMap}
              onOpenFile={onOpenFile}
              onMkdir={onMkdir}
              onRename={onRename}
              onDelete={onDelete}
              busy={busy}
            />
          ))}
        </div>
      )}
    </div>
  )
}

// GitDot git 状态色标
function GitDot({ status }) {
  const color = {
    modified: 'bg-amber-400',
    added: 'bg-green-500',
    deleted: 'bg-red-500',
    untracked: 'bg-blue-400',
    renamed: 'bg-purple-400',
    ignored: 'bg-gray-300',
  }[status] || 'bg-gray-300'
  return <span className={`w-2 h-2 rounded-full flex-shrink-0 ${color}`} title={status} aria-label={status} />
}
