import { useState, useEffect, useMemo } from 'react'
import { X, Loader2, Download, FileWarning } from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { clawFilesApi } from '../api/client'

// AR-11 FileViewer：claw 本地文件预览（文本/Markdown/图片/PDF；其余提供下载）。
// props: { cwd, path, onClose }  path 为相对 cwd 的路径。
// 内容经 clawFilesApi.fetch（带 JWT）取 Blob，按扩展名选渲染方式。
const TEXT_EXTS = new Set([
  '.txt', '.log', '.json', '.js', '.mjs', '.jsx', '.ts', '.tsx', '.go', '.rs', '.java',
  '.kt', '.swift', '.py', '.rb', '.php', '.sh', '.yml', '.yaml', '.toml', '.ini', '.cfg',
  '.xml', '.html', '.css', '.scss', '.sql', '.c', '.cc', '.cpp', '.h', '.hpp', '.vue', '.svelte', '.md'
])
const MD_EXTS = new Set(['.md', '.markdown'])
const IMG_EXTS = new Set(['.png', '.jpg', '.jpeg', '.gif', '.webp', '.svg', '.bmp', '.ico'])
const MAX_TEXT_BYTES = 512 * 1024  // 文本预览上限 512KB

function extOf(path) {
  const i = path.lastIndexOf('.')
  return i >= 0 ? path.slice(i).toLowerCase() : ''
}

export default function FileViewer({ cwd, path, onClose }) {
  const [blob, setBlob] = useState(null)
  const [text, setText] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const ext = useMemo(() => extOf(path || ''), [path])
  const kind = useMemo(() => {
    if (IMG_EXTS.has(ext)) return 'image'
    if (ext === '.pdf') return 'pdf'
    if (MD_EXTS.has(ext)) return 'markdown'
    if (TEXT_EXTS.has(ext)) return 'text'
    return 'binary'
  }, [ext])

  // object URL（图片/pdf/下载用）
  const objectUrl = useMemo(() => (blob ? URL.createObjectURL(blob) : null), [blob])
  useEffect(() => {
    if (objectUrl) return () => URL.revokeObjectURL(objectUrl)
  }, [objectUrl])

  useEffect(() => {
    if (!cwd || !path) return
    let cancelled = false
    setLoading(true)
    setError('')
    setBlob(null)
    setText('')
    clawFilesApi.fetch(cwd, path)
      .then(async (b) => {
        if (cancelled) return
        setBlob(b)
        if (kind === 'text' || kind === 'markdown') {
          if (b.size > MAX_TEXT_BYTES) {
            setError('文件过大，仅提供下载（文本预览上限 512KB）')
            return
          }
          const t = await b.text()
          if (!cancelled) setText(t)
        }
      })
      .catch((e) => { if (!cancelled) setError(e.message || '读取失败') })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [cwd, path, kind])

  const fileName = path ? path.split('/').pop() : ''

  return (
    <div className="flex flex-col h-full bg-white">
      {/* 标题栏 */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-eleball-outline-variant flex-shrink-0">
        <span className="flex-1 min-w-0 text-xs font-medium text-eleball-text truncate" title={path}>{fileName || '预览'}</span>
        {objectUrl && (
          <a href={objectUrl} download={fileName}
            className="p-1 rounded text-eleball-text-secondary hover:bg-gray-100" aria-label="下载" title="下载">
            <Download className="w-3.5 h-3.5" />
          </a>
        )}
        <button type="button" onClick={onClose} aria-label="关闭预览" title="关闭"
          className="p-1 rounded text-eleball-text-secondary hover:bg-gray-100">
          <X className="w-4 h-4" />
        </button>
      </div>

      {/* 内容区 */}
      <div className="flex-1 overflow-auto min-h-0">
        {loading && (
          <div className="flex items-center justify-center h-full text-eleball-text-secondary text-xs">
            <Loader2 className="w-4 h-4 animate-spin mr-2" /> 加载中…
          </div>
        )}
        {error && !loading && (
          <div className="flex flex-col items-center justify-center h-full gap-2 text-eleball-text-secondary text-xs px-6 text-center">
            <FileWarning className="w-6 h-6" />
            <span>{error}</span>
            {objectUrl && (
              <a href={objectUrl} download={fileName}
                className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-eleball-primary text-white">
                <Download className="w-3.5 h-3.5" /> 下载文件
              </a>
            )}
          </div>
        )}
        {!loading && !error && kind === 'image' && objectUrl && (
          <div className="flex items-center justify-center p-4 min-h-full">
            <img src={objectUrl} alt={fileName} className="max-w-full max-h-full object-contain" />
          </div>
        )}
        {!loading && !error && kind === 'pdf' && objectUrl && (
          <iframe src={objectUrl} title={fileName} className="w-full h-full border-0" />
        )}
        {!loading && !error && kind === 'markdown' && (
          <div className="p-4 prose prose-sm max-w-none text-eleball-text">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown>
          </div>
        )}
        {!loading && !error && kind === 'text' && (
          <pre className="text-xs leading-relaxed font-mono text-eleball-text whitespace-pre-wrap break-words p-3 m-0">
            {text}
          </pre>
        )}
        {!loading && !error && kind === 'binary' && (
          <div className="flex flex-col items-center justify-center h-full gap-2 text-eleball-text-secondary text-xs">
            <FileWarning className="w-6 h-6" />
            <span>此格式不支持内联预览</span>
            {objectUrl && (
              <a href={objectUrl} download={fileName}
                className="inline-flex items-center gap-1 px-3 py-1.5 rounded-lg bg-eleball-primary text-white">
                <Download className="w-3.5 h-3.5" /> 下载文件
              </a>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
