import { useEffect, useState } from 'react'
import { Loader2, Film, AlertCircle } from 'lucide-react'
import { useVisualModels } from '../../hooks/useVisualModels'
import ImageUploader from './ImageUploader'

export default function VideoCreationPanel({ onCreate, initialPrompt = '', disabled = false, onModelChange }) {
  const { videoModels, loading: modelsLoading } = useVisualModels('video')
  const [modelName, setModelName] = useState('')
  const [provider, setProvider] = useState('')

  useEffect(() => {
    if (videoModels.length > 0 && !modelName) {
      const first = videoModels[0]
      setModelName(first.model_name)
      setProvider(first.provider)
      onModelChange?.(first)
    }
  }, [videoModels, modelName, onModelChange])

  const [prompt, setPrompt] = useState(initialPrompt)
  const [duration, setDuration] = useState(5)
  const [resolution, setResolution] = useState('720p')
  const [image, setImage] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const hasModels = videoModels.length > 0
  const canSubmit = hasModels && modelName && prompt.trim() && !disabled

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    setError('')
    try {
      await onCreate({
        provider,
        model: modelName,
        prompt: prompt.trim(),
        image_url: image?.status === 'done' ? image.url : undefined,
        duration,
        resolution
      })
      setPrompt('')
      setImage(null)
    } catch (err) {
      setError(err.message || '创建失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {disabled && (
        <div className="flex items-start gap-2 rounded-lg bg-amber-900/30 border border-amber-800/50 px-3 py-2 text-xs text-amber-300">
          <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
          <span>当前有进行中的视频生成任务，请等待完成后再创建新任务。</span>
        </div>
      )}
      <div>
        <label className="block text-sm font-medium text-[#a0a0b8] mb-1">模型</label>
        <select
          value={modelName ? `${provider}:${modelName}` : ''}
          onChange={(e) => {
            const value = e.target.value
            if (!value) {
              setProvider('')
              setModelName('')
              return
            }
            const sepIndex = value.indexOf(':')
            const p = sepIndex > -1 ? value.slice(0, sepIndex) : ''
            const m = sepIndex > -1 ? value.slice(sepIndex + 1) : value
            setProvider(p)
            setModelName(m)
            const selected = videoModels.find((item) => item.provider === p && item.model_name === m)
            onModelChange?.(selected)
          }}
          disabled={modelsLoading || !hasModels}
          className="w-full rounded-lg border border-[#2e2e45] bg-[#252538] px-3 py-2 text-sm text-[#e8e8f0] focus:outline-none focus:ring-2 focus:ring-[#6750A4] disabled:opacity-50"
        >
          {!hasModels && <option value="">暂无可用模型</option>}
          {hasModels && videoModels.map((m) => {
            const value = `${m.provider}:${m.model_name}`
            return (
              <option key={value} value={value}>
                {m.display_name || `${m.provider}/${m.model_name}`}
              </option>
            )
          })}
        </select>
        {!hasModels && !modelsLoading && (
          <div className="mt-2 flex items-start gap-2 rounded-lg bg-amber-900/30 border border-amber-800/50 px-3 py-2 text-xs text-amber-300">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <span>
              管理员尚未配置视频生成模型。请在管理后台「Ele Agent 模型」中启用并勾选「支持视频生成」。
            </span>
          </div>
        )}
      </div>

      <div>
        <label className="block text-sm font-medium text-[#a0a0b8] mb-1">视频描述</label>
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={5}
          placeholder="描述你想要的视频内容、镜头、动作与风格，例如：无人机视角拍摄的海浪拍打礁石，夕阳金光洒落……"
          className="w-full rounded-lg border border-[#2e2e45] bg-[#252538] px-3 py-2 text-sm text-[#e8e8f0] placeholder-[#6e6e8a] focus:outline-none focus:ring-2 focus:ring-[#6750A4] resize-none"
        />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-[#a0a0b8] mb-1">时长（秒）</label>
          <select
            value={duration}
            onChange={(e) => setDuration(Number(e.target.value))}
            className="w-full rounded-lg border border-[#2e2e45] bg-[#252538] px-3 py-2 text-sm text-[#e8e8f0] focus:outline-none focus:ring-2 focus:ring-[#6750A4]"
          >
            <option value={5}>5 秒</option>
            <option value={10}>10 秒</option>
          </select>
        </div>
        <div>
          <label className="block text-sm font-medium text-[#a0a0b8] mb-1">分辨率</label>
          <select
            value={resolution}
            onChange={(e) => setResolution(e.target.value)}
            className="w-full rounded-lg border border-[#2e2e45] bg-[#252538] px-3 py-2 text-sm text-[#e8e8f0] focus:outline-none focus:ring-2 focus:ring-[#6750A4]"
          >
            <option value="720p">720p</option>
            <option value="1080p">1080p</option>
          </select>
        </div>
      </div>

      <ImageUploader value={image} onChange={setImage} label="首帧图（可选）" />

      {error && <p className="text-sm text-[#ff7b7b]">{error}</p>}

      <button
        type="submit"
        disabled={submitting || !canSubmit}
        title={!hasModels ? '请先配置视频生成模型' : !modelName ? '请选择模型' : !prompt.trim() ? '请输入提示词' : ''}
        className="w-full flex items-center justify-center gap-2 rounded-lg bg-[#6750A4] px-4 py-2.5 text-sm font-medium text-white hover:bg-[#4a3b7a] disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        {submitting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Film className="w-4 h-4" />}
        {submitting ? '提交中……' : '生成视频'}
      </button>
    </form>
  )
}
