import { useEffect, useState } from 'react'
import { Download, Film, Loader2 } from 'lucide-react'
import { formatCost, resolveVisualUrl, STATUS_COLORS, STATUS_LABELS } from '../../utils/visualTasks'

export default function VideoPreviewPanel({ task }) {
  const [videoFailed, setVideoFailed] = useState(false)

  // 切换任务时重置失败状态，避免上一个任务的视频加载失败误导新任务
  useEffect(() => {
    setVideoFailed(false)
  }, [task?.id])

  if (!task) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-[#6e6e8a]">
        <Film className="w-12 h-12 mb-3 opacity-50" />
        <p className="text-sm">在左侧输入提示词并生成视频</p>
      </div>
    )
  }

  let result
  try {
    result = task.result ? JSON.parse(task.result) : null
  } catch {
    result = null
  }

  const url = result?.url ? resolveVisualUrl(result.url) : undefined
  const coverUrl = result?.cover_url ? resolveVisualUrl(result.cover_url) : undefined

  return (
    <div className="flex h-full flex-col">
      <div className="mb-3 flex items-center justify-between">
        <div className="text-sm text-[#a0a0b8]">
          状态：<span className={STATUS_COLORS[task.status] || 'text-[#a0a0b8]'}>{STATUS_LABELS[task.status] || task.status}</span>
        </div>
        <div className="text-sm text-[#6e6e8a]">消耗：{formatCost(task.cost, task.currency)}</div>
      </div>

      {task.status === 'running' || task.status === 'pending' ? (
        <div className="flex flex-1 flex-col items-center justify-center text-[#a0a0b8]">
          <Loader2 className="w-8 h-8 animate-spin mb-2 text-[#b8a5ff]" />
          <span className="text-sm">
            {task.status === 'pending' ? '排队等待视频资源，请稍候……' : '视频生成中，请稍候……'}
          </span>
          <p className="mt-1 text-xs text-[#6e6e8a]">可继续浏览其他页面，完成后可在任务列表查看</p>
        </div>
      ) : task.status === 'failed' ? (
        <div className="flex flex-1 items-center justify-center text-[#ff7b7b] text-sm">
          {task.error_message || '生成失败，请稍后重试'}
        </div>
      ) : (
        <>
          <div className="relative flex-1 rounded-xl overflow-hidden bg-[#13131f] border border-[#2e2e45] flex items-center justify-center">
            {url && !videoFailed ? (
              <video
                src={url}
                controls
                className="max-h-full max-w-full"
                poster={coverUrl}
                onError={() => setVideoFailed(true)}
              />
            ) : videoFailed ? (
              <div className="flex flex-col items-center text-[#6e6e8a]">
                <Film className="w-10 h-10 mb-2 opacity-50" />
                <span className="text-sm">视频链接已失效，请重新生成</span>
              </div>
            ) : (
              <span className="text-sm text-[#6e6e8a]">无可用视频</span>
            )}
          </div>

          {url && !videoFailed && (
            <div className="mt-3 space-y-2">
              <a
                href={url}
                download
                className="inline-flex w-full items-center justify-center gap-2 rounded-lg bg-[#6750A4] px-4 py-2 text-sm text-white hover:bg-[#4a3b7a] transition-colors"
              >
                <Download className="w-4 h-4" />
                下载视频
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
