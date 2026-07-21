import { useEffect, useRef } from 'react'
import { Image as ImageIcon, Film, Loader2, User, Bot, ArrowRight } from 'lucide-react'
import { formatCost, isTerminal, resolveVisualUrl, STATUS_COLORS, STATUS_LABELS } from '../../utils/visualTasks'

function parseResult(resultStr) {
  if (!resultStr) return null
  try {
    return JSON.parse(resultStr)
  } catch {
    return null
  }
}

function TaskResultCard({ task, onSelect, onContinue }) {
  const result = parseResult(task.result)
  const urls = (result?.urls || (result?.url ? [result.url] : [])).map(resolveVisualUrl)
  const coverUrl = result?.cover_url ? resolveVisualUrl(result.cover_url) : undefined
  const isVideo = task.media_type === 'video'
  const canContinue = isTerminal(task.status) && task.status === 'succeeded' && urls.length > 0

  return (
    <div
      onClick={() => onSelect?.(task)}
      className="group cursor-pointer rounded-xl border border-[#2e2e45] bg-[#1c1c2b] p-3 hover:border-[#6750A4]/50 transition-colors max-w-[80%]"
    >
      {/* 状态与成本 */}
      <div className="mb-2 flex items-center justify-between text-xs">
        <span className={`font-medium ${STATUS_COLORS[task.status] || 'text-[#a0a0b8]'}`}>
          {STATUS_LABELS[task.status] || task.status}
        </span>
        <span className="text-[#6e6e8a]">消耗 {formatCost(task.cost, task.currency)}</span>
      </div>

      {/* 结果预览 */}
      {task.status === 'pending' || task.status === 'running' ? (
        <div className="flex items-center gap-2 text-sm text-[#a0a0b8]">
          <Loader2 className="w-4 h-4 animate-spin" />
          {task.status === 'pending'
            ? (isVideo ? '排队等待视频资源……' : '排队等待生成资源……')
            : (isVideo ? '视频生成中……' : '图片生成中……')}
        </div>
      ) : task.status === 'failed' ? (
        <div className="text-sm text-[#ff7b7b]">{task.error_message || '生成失败'}</div>
      ) : urls.length > 0 ? (
        <div className="space-y-2">
          <div className="rounded-lg overflow-hidden bg-[#13131f]">
            {isVideo ? (
              <video src={urls[0]} controls className="max-w-full max-h-[240px] object-contain" poster={coverUrl} />
            ) : (
              <img src={urls[0]} alt="生成结果" className="max-w-full max-h-[240px] object-contain" />
            )}
          </div>
          {urls.length > 1 && (
            <div className="flex gap-2 overflow-x-auto">
              {urls.map((u, idx) => (
                <img key={idx} src={u} alt={`结果 ${idx + 1}`} className="w-16 h-16 object-cover rounded-lg border border-[#2e2e45]" />
              ))}
            </div>
          )}
        </div>
      ) : (
        <div className="text-sm text-[#6e6e8a]">无可用结果</div>
      )}

      {canContinue && onContinue && (
        <button
          onClick={(e) => {
            e.stopPropagation()
            onContinue(task)
          }}
          className="mt-2 flex items-center gap-1 text-xs text-[#b8a5ff] hover:text-white transition-colors"
        >
          <ArrowRight className="w-3 h-3" />
          {isVideo ? '基于此视频延长' : '基于此图继续创作'}
        </button>
      )}
    </div>
  )
}

export default function VisualChatThread({ tasks, selectedTask, onSelectTask, onContinueTask, showMemoryWarning, mediaType }) {
  const bottomRef = useRef(null)

  // 按创建时间升序排列，新消息在底部
  const sortedTasks = [...tasks].sort((a, b) => new Date(a.created_at) - new Date(b.created_at))

  // 新任务生成后自动滚动到底部
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [tasks.length, tasks.map((t) => t.status).join(',')])

  if (tasks.length === 0) {
    return (
      <div className="flex h-full flex-col items-center justify-center text-[#6e6e8a]">
        <div className="mb-3 rounded-full bg-[#252538] p-4">
          {mediaType === 'video' ? <Film className="w-8 h-8 opacity-50" /> : <ImageIcon className="w-8 h-8 opacity-50" />}
        </div>
        <p className="text-sm">在下方输入描述，开始{mediaType === 'video' ? '视频' : '图片'}创作</p>
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col overflow-y-auto p-4 space-y-6">
      {sortedTasks.map((task) => {
        const isSelected = selectedTask?.id === task.id
        return (
          <div key={task.id} className="flex flex-col gap-3">
            {/* 用户 Prompt */}
            <div className="flex items-start gap-3 justify-end">
              <div className="max-w-[80%] rounded-2xl rounded-tr-sm bg-[#6750A4] px-4 py-2.5 text-sm text-white">
                {task.prompt || '未填写提示词'}
              </div>
              <div className="flex-shrink-0 rounded-full bg-[#6750A4]/20 p-1.5">
                <User className="w-4 h-4 text-[#b8a5ff]" />
              </div>
            </div>

            {/* 助手结果 */}
            <div className="flex items-start gap-3">
              <div className="flex-shrink-0 rounded-full bg-[#252538] p-1.5">
                <Bot className="w-4 h-4 text-[#a0a0b8]" />
              </div>
              <div className="flex-1 min-w-0">
                <div
                  className={`inline-block transition-opacity ${isSelected ? 'opacity-100' : 'opacity-90'}`}
                >
                  <TaskResultCard task={task} onSelect={onSelectTask} onContinue={onContinueTask} />
                </div>
              </div>
            </div>
          </div>
        )
      })}
      {showMemoryWarning && (
        <div className="flex items-start gap-2 rounded-lg bg-amber-900/30 border border-amber-800/50 px-3 py-2 text-xs text-amber-300">
          <span>当前模型不支持对话记忆，请每次输入尽量完整的创作要求；如需连续创作，请联系管理员配置「视觉 Prompt 融合模型」。</span>
        </div>
      )}
      <div ref={bottomRef} />
    </div>
  )
}
