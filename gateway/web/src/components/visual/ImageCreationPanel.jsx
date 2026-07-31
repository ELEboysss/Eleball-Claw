import { useEffect, useState } from 'react'
import { Loader2, Wand2, AlertCircle } from 'lucide-react'
import { useVisualModels } from '../../hooks/useVisualModels'
import ImageUploader from './ImageUploader'

export default function ImageCreationPanel({ onCreate, initialPrompt = '', disabled = false, onModelChange }) {
  const { imageModels, loading: modelsLoading } = useVisualModels('image')
  const [modelName, setModelName] = useState('')
  const [provider, setProvider] = useState('')

  useEffect(() => {
    if (imageModels.length > 0 && !modelName) {
      const first = imageModels[0]
      setModelName(first.model_name)
      setProvider(first.provider)
      onModelChange?.(first)
    }
  }, [imageModels, modelName, onModelChange])

  const [prompt, setPrompt] = useState(initialPrompt)
  const [size, setSize] = useState('1024x1024')
  const [quality, setQuality] = useState('standard')
  const [n, setN] = useState(1)
  const [image, setImage] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const hasModels = imageModels.length > 0
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
        size,
        quality,
        n
      })
      setPrompt('')
      setImage(null)
    } catch (err) {
      setError(err.message || '生成失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      {disabled && (
        <div className="flex items-start gap-2 rounded-lg bg-amber-900/30 border border-amber-800/50 px-3 py-2 text-xs text-amber-300">
          <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
          <span>当前有进行中的图片生成任务，请等待完成后再创建新任务。</span>
        </div>
      )}
      <div>
        <label className="block text-sm font-medium text-eleball-vs-text-muted mb-1">模型</label>
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
            const selected = imageModels.find((item) => item.provider === p && item.model_name === m)
            onModelChange?.(selected)
          }}
          disabled={modelsLoading || !hasModels}
          className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text focus:outline-none focus:ring-2 focus:ring-eleball-primary disabled:opacity-50"
        >
          {!hasModels && <option value="">暂无可用模型</option>}
          {hasModels && imageModels.map((m) => {
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
              管理员尚未配置图片生成模型。请在管理后台「Ele Agent 模型」中启用并勾选「支持图片生成」。
            </span>
          </div>
        )}
      </div>

      <div>
        <label className="block text-sm font-medium text-eleball-vs-text-muted mb-1">描述提示词</label>
        <textarea
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={5}
          placeholder="描述你想要的画面，例如：一只戴着墨镜的猫坐在沙滩上看日落……"
          className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text placeholder-eleball-vs-text-dim focus:outline-none focus:ring-2 focus:ring-eleball-primary resize-none"
        />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-eleball-vs-text-muted mb-1">尺寸</label>
          <select
            value={size}
            onChange={(e) => setSize(e.target.value)}
            className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text focus:outline-none focus:ring-2 focus:ring-eleball-primary"
          >
            <option value="1024x1024">1024 × 1024</option>
            <option value="1024x1536">1024 × 1536</option>
            <option value="1536x1024">1536 × 1024</option>
          </select>
        </div>
        <div>
          <label className="block text-sm font-medium text-eleball-vs-text-muted mb-1">画质</label>
          <select
            value={quality}
            onChange={(e) => setQuality(e.target.value)}
            className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text focus:outline-none focus:ring-2 focus:ring-eleball-primary"
          >
            <option value="standard">标准</option>
            <option value="hd">高清</option>
          </select>
        </div>
      </div>

      <div>
        <label className="block text-sm font-medium text-eleball-vs-text-muted mb-1">数量</label>
        <input
          type="number"
          min={1}
          max={4}
          value={n}
          onChange={(e) => setN(Math.max(1, Math.min(4, Number(e.target.value))))}
          className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text focus:outline-none focus:ring-2 focus:ring-eleball-primary"
        />
      </div>

      <ImageUploader value={image} onChange={setImage} />

      {error && <p className="text-sm text-eleball-vs-error">{error}</p>}

      <button
        type="submit"
        disabled={submitting || !canSubmit}
        title={!hasModels ? '请先配置图片生成模型' : !modelName ? '请选择模型' : !prompt.trim() ? '请输入提示词' : ''}
        className="w-full flex items-center justify-center gap-2 rounded-lg bg-eleball-primary px-4 py-2.5 text-sm font-medium text-white hover:bg-eleball-vs-primary-hover disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        {submitting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Wand2 className="w-4 h-4" />}
        {submitting ? '生成中……' : '生成图片'}
      </button>
    </form>
  )
}
