import { useState } from 'react'
import { Download, ImageOff, Loader2, ZoomIn } from 'lucide-react'
import { formatCost, isTerminal, resolveVisualUrl, STATUS_COLORS, STATUS_LABELS } from '../../utils/visualTasks'

export default function ImagePreviewPanel({ task }) {
  const [previewUrl, setPreviewUrl] = useState(null)
  const [failedUrls, setFailedUrls] = useState(new Set())

  if (!task) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-[#6e6e8a]">
        <ImageOff className="w-12 h-12 mb-3 opacity-50" />
        <p className="text-sm">在左侧输入提示词并生成图片</p>
      </div>
    )
  }

  let result
  try {
    result = task.result ? JSON.parse(task.result) : null
  } catch {
    result = null
  }

  const rawUrls = result?.urls || (result?.url ? [result.url] : [])
  const urls = rawUrls.map(resolveVisualUrl)
  const activeUrl = previewUrl || urls[0]
  const activeFailed = activeUrl ? failedUrls.has(activeUrl) : false

  const handleImageError = (url) => {
    setFailedUrls((prev) => new Set(prev).add(url))
  }

  return (
    <div className="flex h-full flex-col">
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm text-[#a0a0b8]">
          状态：<span className={STATUS_COLORS[task.status] || 'text-[#a0a0b8]'}>{STATUS_LABELS[task.status] || task.status}</span>
        </div>
        <div className="text-sm text-[#6e6e8a]">消耗：{formatCost(task.cost, task.currency)}</div>
      </div>

      {task.status === 'running' || task.status === 'pending' ? (
        <div className="flex flex-1 items-center justify-center text-[#a0a0b8]">
          <Loader2 className="w-8 h-8 animate-spin mr-2 text-[#b8a5ff]" />
          <span className="text-sm">
            {task.status === 'pending' ? '排队等待生成资源，请稍候……' : '图片生成中，请稍候……'}
          </span>
        </div>
      ) : task.status === 'failed' ? (
        <div className="flex flex-1 items-center justify-center text-[#ff7b7b] text-sm">
          {task.error_message || '生成失败，请稍后重试'}
        </div>
      ) : (
        <>
          <div className="relative flex-1 rounded-xl overflow-hidden bg-[#13131f] border border-[#2e2e45] flex items-center justify-center">
            {activeUrl && !activeFailed ? (
              <>
                <img
                  src={activeUrl}
                  alt="生成结果"
                  className="max-h-full max-w-full object-contain"
                  onError={() => handleImageError(activeUrl)}
                />
                <a
                  href={activeUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="absolute top-2 right-2 p-2 rounded-lg bg-slate-800/80 text-slate-200 hover:bg-slate-700"
                  title="原图查看"
                >
                  <ZoomIn className="w-4 h-4" />
                </a>
              </>
            ) : activeFailed ? (
              <div className="flex flex-col items-center text-[#6e6e8a]">
                <ImageOff className="w-10 h-10 mb-2 opacity-50" />
                <span className="text-sm">图片链接已失效，请重新生成</span>
              </div>
            ) : (
              <span className="text-sm text-[#6e6e8a]">无可用图片</span>
            )}
          </div>

          {urls.length > 1 && (
            <div className="mt-3 flex gap-2 overflow-x-auto pb-1">
              {urls.map((url, idx) => (
                <button
                  key={idx}
                  onClick={() => setPreviewUrl(url)}
                  className={`flex-shrink-0 w-16 h-16 rounded-lg overflow-hidden border-2 ${activeUrl === url ? 'border-[#6750A4]' : 'border-[#2e2e45]'}`}
                >
                  <img
                    src={url}
                    alt={`缩略图 ${idx + 1}`}
                    className="w-full h-full object-cover"
                    onError={() => handleImageError(url)}
                  />
                </button>
              ))}
            </div>
          )}

          {activeUrl && !activeFailed && (
            <div className="mt-3 space-y-2">
              <a
                href={activeUrl}
                download
                className="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-[#6750A4] px-4 py-2 text-sm text-white hover:bg-[#4a3b7a] transition-colors"
              >
                <Download className="w-4 h-4" />
                下载图片
              </a>
              <p className="text-xs text-[#6e6e8a] text-center">
                下载链接可能因时效而过期，请尽早下载以防丢失
              </p>
            </div>
          )}
        </>
      )}
    </div>
  )
}
