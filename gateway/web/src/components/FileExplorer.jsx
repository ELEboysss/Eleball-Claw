import { useState, useEffect, useMemo, useCallback } from 'react'
import { ChevronRight, ChevronDown, Folder, FolderOpen, File as FileIcon, Loader2, RefreshCw } from 'lucide-react'
import { clawFilesApi } from '../api/client'

// AR-11 FileExplorer：claw 本地文件树（懒加载展开 + git 状态色标）。
// props:
//   cwd        工作目录绝对路径（沙箱根）
//   onOpenFile(relPath)  点击文件回调
//   gitStatus  { is_repo, entries: [{path,x,y,status}] } 可空
//   refreshKey 变化时重载根（外部触发刷新）
export default function FileExplorer({ cwd, onOpenFile, gitStatus, refreshKey }) {
  const [rootEntries, setRootEntries] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

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

  useEffect(() => { loadRoot() }, [loadRoot, refreshKey])

  if (!cwd) {
    return <div className="p-4 text-xs text-eleball-text-secondary">未设置工作目录</div>
  }

  return (
    <div className="flex flex-col h-full text-xs">
      <div className="flex items-center justify-between px-3 py-2 border-b border-eleball-outline-variant">
        <span className="font-medium text-eleball-text truncate" title={cwd}>文件</span>
        <button type="button" onClick={loadRoot} aria-label="刷新文件树" title="刷新"
          className="p-1 rounded text-eleball-text-secondary hover:bg-gray-100">
          <RefreshCw className="w-3.5 h-3.5" />
        </button>
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
          />
        ))}
      </div>
    </div>
  )
}

// TreeNode 单节点：目录可展开懒加载子项；文件可点击打开。
function TreeNode({ cwd, relPath, name, isDir, depth, statusMap, onOpenFile }) {
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

  return (
    <div>
      <button
        type="button"
        onClick={toggle}
        style={pad}
        className="w-full flex items-center gap-1 py-1 pr-2 text-left text-eleball-text hover:bg-gray-50"
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
        <span className="truncate flex-1">{name}</span>
        {status && <GitDot status={status} />}
      </button>
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
