import { useEffect, useRef, useState } from 'react'
import { ChevronDown, ChevronUp, ImagePlus, Loader2, Send, X, Film, Image as ImageIcon } from 'lucide-react'
import { useVisualModels } from '../../hooks/useVisualModels'
import { visualApi } from '../../api/client'

// agnes_video 分辨率档位 → 协议 width/height 字段映射
const AGNES_VIDEO_RESOLUTION_PRESETS = {
  '720p': { width: 1280, height: 720 },
  '1080p': { width: 1920, height: 1080 }
}

export default function VisualCreationInput({
  tab,
  disabled = false,
  collapsed: initialCollapsed = false,
  onCreate,
  onModelChange,
  initialPrompt = '',
  continuation = null
}) {
  const mediaType = tab === 'video' ? 'video' : 'image'
  const { imageModels, videoModels, loading: modelsLoading } = useVisualModels(mediaType)
  const models = mediaType === 'video' ? videoModels : imageModels

  const [modelName, setModelName] = useState('')
  const [provider, setProvider] = useState('')
  const [prompt, setPrompt] = useState(initialPrompt)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')


  // 图片参数（按协议对齐：seedream 支持 1K/2K/4K 档位或「宽x高」像素尺寸 + watermark；agnes_image 支持像素尺寸）
  const [size, setSize] = useState('1024x1024')
  const [watermark, setWatermark] = useState(false)
  const [image, setImage] = useState(null)

  // 视频参数（按协议对齐：seedance 支持 ratio/duration/resolution/generate_audio/watermark；
  // agnes_video 支持 duration/width/height，分辨率档位会映射为 width/height）
  const [duration, setDuration] = useState(5)
  const [resolution, setResolution] = useState('720p')
  const [ratio, setRatio] = useState('16:9')
  const [generateAudio, setGenerateAudio] = useState(false)

  // 顶部参数面板折叠/展开
  const [paramsOpen, setParamsOpen] = useState(false)

  const inputRef = useRef(null)
  const fileInputRef = useRef(null)

  // 自动调整输入框高度
  const adjustInputHeight = () => {
    const el = inputRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 128)}px`
  }

  // 处理「基于此图/视频继续创作」的接续状态
  useEffect(() => {
    if (!continuation) return
    if (continuation.prompt) {
      setPrompt(continuation.prompt)
    }
    if (continuation.image_url) {
      setImage({ status: 'done', url: continuation.image_url })
    }
    // 展开参数面板，方便用户确认参考图与模型
    setParamsOpen(true)
    inputRef.current?.focus()
  }, [continuation])

  // 切换图片/视频 tab 时重置已选模型，避免把视频模型带到图片生成
  useEffect(() => {
    setModelName('')
    setProvider('')
  }, [mediaType])

  useEffect(() => {
    if (models.length > 0 && !modelName) {
      const first = models[0]
      setModelName(first.model_name)
      setProvider(first.provider)
      onModelChange?.(first)
    }
  }, [models, modelName, onModelChange])

  // 当前选中模型及其协议（协议决定可选参数字段，必须与上游 API 对齐）
  const currentModel = models.find((m) => m.provider === provider && m.model_name === modelName)
  const protocol = currentModel?.protocol || ''

  // 切换模型/协议时把参数重置为协议安全默认值，避免把 A 协议的参数带到 B 协议
  useEffect(() => {
    if (mediaType === 'image') {
      setSize(protocol === 'seedream' ? '2K' : '1024x1024')
    } else {
      setResolution('720p')
      setRatio('16:9')
      setGenerateAudio(false)
    }
    setWatermark(false)
    // 时长对齐模型配置范围（未配置上限时默认 5 秒）
    const maxD = currentModel?.video_max_duration || 0
    const minD = Math.max(currentModel?.video_min_duration || 0, 1)
    setDuration(maxD > 0 ? Math.min(minD, maxD) : 5)
  }, [protocol, mediaType, currentModel?.video_min_duration, currentModel?.video_max_duration])

  // Seedance 的 adaptive 比例仅在图生视频（有首帧参考图）时有效；文生视频自动回退 16:9
  useEffect(() => {
    if (mediaType === 'video' && protocol === 'seedance' && ratio === 'adaptive' && image?.status !== 'done') {
      setRatio('16:9')
    }
  }, [mediaType, protocol, ratio, image])



  useEffect(() => {
    adjustInputHeight()
  }, [prompt])

  const hasModels = models.length > 0
  const canSubmit = hasModels && modelName && prompt.trim() && !disabled && !submitting

  const handleModelChange = (value) => {
    if (!value) {
      setProvider('')
      setModelName('')
      onModelChange?.(null)
      return
    }
    const sepIndex = value.indexOf(':')
    const p = sepIndex > -1 ? value.slice(0, sepIndex) : ''
    const m = sepIndex > -1 ? value.slice(sepIndex + 1) : value
    setProvider(p)
    setModelName(m)
    const selected = models.find((item) => item.provider === p && item.model_name === m)
    onModelChange?.(selected)
  }

  const handleFileChange = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImage({ status: 'uploading', url: URL.createObjectURL(file), file })
    try {
      const res = await visualApi.upload(file)
      // axios 拦截器已剥离外层 { code, message, data }，res 直接是 data
      setImage({ status: 'done', url: res.url, id: res.id })
    } catch (err) {
      setError(err.message || '上传失败')
      setImage(null)
    }
  }

  const clearImage = () => {
    setImage(null)
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  const handleSubmit = async () => {
    if (!canSubmit) return
    setSubmitting(true)
    setError('')
    try {
      // 生成参数统一放入 params 对象，字段与所选模型的上游协议对齐（后端按协议透传给 Provider）
      const params = {}
      if (mediaType === 'image') {
        params.size = size
        if (protocol === 'seedream') {
          params.watermark = watermark
        }
      } else {
        params.duration = duration
        if (protocol === 'seedance') {
          params.ratio = ratio
          params.resolution = resolution
          params.generate_audio = generateAudio
          params.watermark = watermark
        } else {
          // agnes_video 等：分辨率档位映射为协议字段 width/height
          const preset = AGNES_VIDEO_RESOLUTION_PRESETS[resolution]
          if (preset) {
            params.width = preset.width
            params.height = preset.height
          }
        }
      }
      const payload = {
        provider,
        model: modelName,
        prompt: prompt.trim(),
        image_url: image?.status === 'done' ? image.url : undefined,
        params
      }
      await onCreate(payload)
      setPrompt('')
      setImage(null)
      if (fileInputRef.current) fileInputRef.current.value = ''
      // 发送成功后折叠参数面板
      setParamsOpen(false)
    } catch (err) {
      setError(err.message || '生成失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSubmit()
    }
  }

  return (
    <div className="relative bg-eleball-vs-surface border-t border-eleball-vs-border">
      {/* 顶部浮动参数面板 */}
      {paramsOpen && (
        <div className="absolute bottom-full left-0 right-0 z-20 mb-2 mx-4 rounded-xl border border-eleball-vs-border bg-eleball-vs-surface p-4 shadow-2xl">
          <div className="flex items-center justify-between mb-3">
            <div className="flex items-center gap-2 text-sm font-semibold text-eleball-vs-text-muted">
              {mediaType === 'video' ? <Film className="w-4 h-4" /> : <ImageIcon className="w-4 h-4" />}
              {mediaType === 'video' ? '视频创作' : '图片创作'}
            </div>
            <button
              onClick={() => setParamsOpen(false)}
              className="p-1.5 rounded-md text-eleball-vs-text-muted hover:bg-eleball-primary/20 hover:text-eleball-vs-accent transition-colors"
              title="收起参数"
            >
              <ChevronDown className="w-4 h-4" />
            </button>
          </div>

          {disabled && (
            <div className="mb-3 flex items-start gap-2 rounded-lg bg-amber-900/30 border border-amber-800/50 px-3 py-2 text-xs text-amber-300">
              <span>当前有进行中的{mediaType === 'video' ? '视频' : '图片'}生成任务，请等待完成后再创建新任务。</span>
            </div>
          )}

          <div className="space-y-3">
            <div>
              <label className="block text-xs font-medium text-eleball-vs-text-muted mb-1">模型</label>
              <select
                value={modelName ? `${provider}:${modelName}` : ''}
                onChange={(e) => handleModelChange(e.target.value)}
                disabled={modelsLoading || !hasModels}
                className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text focus:outline-none focus:ring-2 focus:ring-eleball-primary disabled:opacity-50"
              >
                {!hasModels && <option value="">暂无可用模型</option>}
                {hasModels && models.map((m) => {
                  const value = `${m.provider}:${m.model_name}`
                  return (
                    <option key={value} value={value}>
                      {m.display_name || `${m.provider}/${m.model_name}`}
                    </option>
                  )
                })}
              </select>
            </div>

            {mediaType === 'image' ? (
              <>
                <div>
                  <label className="block text-xs font-medium text-eleball-vs-text-muted mb-1">尺寸</label>
                  {protocol === 'seedream' ? (
                    // Seedream（即梦）协议：size 支持 1K/2K/4K 档位或「宽x高」像素尺寸（1280x720 ~ 4096x4096）
                    <select
                      value={size}
                      onChange={(e) => setSize(e.target.value)}
                      className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                    >
                      <optgroup label="档位（比例由模型根据画面决定）">
                        <option value="2K">2K 标准（推荐）</option>
                        <option value="1K">1K 快速</option>
                        <option value="4K">4K 超清</option>
                      </optgroup>
                      <optgroup label="固定尺寸">
                        <option value="2048x2048">1:1 · 2048×2048</option>
                        <option value="2304x1728">4:3 · 2304×1728</option>
                        <option value="1728x2304">3:4 · 1728×2304</option>
                        <option value="2496x1664">3:2 · 2496×1664</option>
                        <option value="1664x2496">2:3 · 1664×2496</option>
                        <option value="2560x1440">16:9 · 2560×1440</option>
                        <option value="1440x2560">9:16 · 1440×2560</option>
                        <option value="3024x1296">21:9 · 3024×1296</option>
                      </optgroup>
                    </select>
                  ) : (
                    <select
                      value={size}
                      onChange={(e) => setSize(e.target.value)}
                      className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                    >
                      <option value="1024x1024">1024 × 1024</option>
                      <option value="1024x1536">1024 × 1536</option>
                      <option value="1536x1024">1536 × 1024</option>
                    </select>
                  )}
                </div>
                {protocol === 'seedream' && (
                  <label className="flex items-center gap-2 text-xs text-eleball-vs-text-muted cursor-pointer">
                    <input
                      type="checkbox"
                      checked={watermark}
                      onChange={(e) => setWatermark(e.target.checked)}
                      className="w-3.5 h-3.5 rounded border-eleball-vs-border-variant bg-eleball-vs-surface-variant"
                    />
                    添加水印（watermark，默认关闭）
                  </label>
                )}
              </>
            ) : (
              <>
                <div className="grid grid-cols-2 gap-3">
                  {protocol === 'seedance' && (
                    <div>
                      <label className="block text-xs font-medium text-eleball-vs-text-muted mb-1">比例</label>
                      <select
                        value={ratio}
                        onChange={(e) => setRatio(e.target.value)}
                        className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                      >
                        {/* adaptive 仅图生视频（有首帧参考图）时被上游接受 */}
                        {image?.status === 'done' && (
                          <option value="adaptive">自适应（跟随首帧图）</option>
                        )}
                        <option value="16:9">16:9 横屏</option>
                        <option value="9:16">9:16 竖屏</option>
                        <option value="1:1">1:1 方形</option>
                        <option value="4:3">4:3</option>
                        <option value="3:4">3:4</option>
                        <option value="21:9">21:9 宽屏</option>
                      </select>
                    </div>
                  )}
                  <div>
                    <label className="block text-xs font-medium text-eleball-vs-text-muted mb-1">时长（秒）</label>
                    {currentModel?.video_max_duration > 0 ? (
                      <select
                        value={duration}
                        onChange={(e) => setDuration(Number(e.target.value))}
                        className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                      >
                        {(() => {
                          const options = []
                          const min = Math.max(currentModel.video_min_duration || 1, 1)
                          const max = currentModel.video_max_duration
                          const step = currentModel.video_duration_step || 1
                          for (let d = min; d <= max; d += step) {
                            options.push(<option key={d} value={d}>{d} 秒</option>)
                          }
                          return options
                        })()}
                      </select>
                    ) : (
                      <input
                        type="number"
                        min={1}
                        value={duration}
                        onChange={(e) => setDuration(Math.max(1, Number(e.target.value)))}
                        className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                        placeholder="自定义时长"
                      />
                    )}
                  </div>
                  <div>
                    <label className="block text-xs font-medium text-eleball-vs-text-muted mb-1">分辨率</label>
                    {protocol === 'seedance' ? (
                      // Seedance 协议：resolution 取值 480p / 720p / 1080p
                      <select
                        value={resolution}
                        onChange={(e) => setResolution(e.target.value)}
                        className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                      >
                        <option value="480p">480p</option>
                        <option value="720p">720p</option>
                        <option value="1080p">1080p</option>
                      </select>
                    ) : (
                      // agnes_video 等：档位会映射为协议字段 width/height
                      <select
                        value={resolution}
                        onChange={(e) => setResolution(e.target.value)}
                        className="w-full rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2 text-sm text-eleball-vs-text"
                      >
                        <option value="720p">720p（1280×720）</option>
                        <option value="1080p">1080p（1920×1080）</option>
                      </select>
                    )}
                  </div>
                </div>
                {protocol === 'seedance' && (
                  <div className="flex items-center gap-4">
                    <label className="flex items-center gap-2 text-xs text-eleball-vs-text-muted cursor-pointer">
                      <input
                        type="checkbox"
                        checked={generateAudio}
                        onChange={(e) => setGenerateAudio(e.target.checked)}
                        className="w-3.5 h-3.5 rounded border-eleball-vs-border-variant bg-eleball-vs-surface-variant"
                      />
                      生成音频（仅 Seedance 1.5 等模型支持）
                    </label>
                    <label className="flex items-center gap-2 text-xs text-eleball-vs-text-muted cursor-pointer">
                      <input
                        type="checkbox"
                        checked={watermark}
                        onChange={(e) => setWatermark(e.target.checked)}
                        className="w-3.5 h-3.5 rounded border-eleball-vs-border-variant bg-eleball-vs-surface-variant"
                      />
                      添加水印（默认关闭）
                    </label>
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      )}

      {/* 已上传图片缩略图 */}
      {image && (
        <div className="px-4 pt-3">
          <div className="inline-flex items-center gap-2 rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-2 py-1.5">
            {image.status === 'uploading' ? (
              <Loader2 className="w-4 h-4 animate-spin text-eleball-vs-accent" />
            ) : (
              <img src={image.url} alt="参考图" className="w-8 h-8 rounded object-cover" />
            )}
            <span className="text-xs text-eleball-vs-text-muted">{image.status === 'uploading' ? '上传中…' : '参考图'}</span>
            <button
              onClick={clearImage}
              className="p-1 rounded-md text-eleball-vs-text-dim hover:text-eleball-vs-error hover:bg-eleball-vs-error/10"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>
      )}

      {/* 底部输入栏 */}
      <div className="flex items-end gap-2 p-3">
        {/* 参数展开/收起按钮 */}
        <button
          onClick={() => setParamsOpen((prev) => !prev)}
          className={`flex-shrink-0 p-2.5 rounded-lg border transition-colors ${
            paramsOpen
              ? 'border-eleball-primary bg-eleball-primary/20 text-eleball-vs-accent'
              : 'border-eleball-vs-border-variant bg-eleball-vs-surface-variant text-eleball-vs-text-muted hover:bg-eleball-primary/10 hover:text-eleball-vs-accent'
          }`}
          title="创作参数"
        >
          {paramsOpen ? <ChevronDown className="w-5 h-5" /> : <ChevronUp className="w-5 h-5" />}
        </button>

        {/* 上传参考图按钮：仅当当前模型支持图片输入时显示 */}
        {currentModel?.supports_image_input && (
          <>
            <button
              onClick={() => fileInputRef.current?.click()}
              disabled={image?.status === 'uploading'}
              className={`flex-shrink-0 p-2.5 rounded-lg border transition-colors ${
                image
                  ? 'border-eleball-primary bg-eleball-primary/20 text-eleball-vs-accent'
                  : 'border-eleball-vs-border-variant bg-eleball-vs-surface-variant text-eleball-vs-text-muted hover:bg-eleball-primary/10 hover:text-eleball-vs-accent'
              } disabled:opacity-50`}
              title="上传参考图"
            >
              <ImagePlus className="w-5 h-5" />
            </button>
            <input
              ref={fileInputRef}
              type="file"
              accept="image/png,image/jpeg,image/webp"
              onChange={handleFileChange}
              className="hidden"
            />
          </>
        )}

        {/* 输入框 */}
        <textarea
          ref={inputRef}
          value={prompt}
          onChange={(e) => {
            setPrompt(e.target.value)
            adjustInputHeight()
          }}
          onKeyDown={handleKeyDown}
          rows={1}
          placeholder={`描述你想要的${mediaType === 'video' ? '视频' : '画面'}，按 Enter 发送...`}
          className="flex-1 max-h-32 min-h-[44px] rounded-lg border border-eleball-vs-border-variant bg-eleball-vs-surface-variant px-3 py-2.5 text-sm text-eleball-vs-text placeholder-eleball-vs-text-dim focus:outline-none focus:ring-2 focus:ring-eleball-primary resize-none"
        />

        {/* 发送按钮 */}
        <button
          onClick={handleSubmit}
          disabled={!canSubmit}
          className="flex-shrink-0 flex items-center justify-center gap-1.5 rounded-lg bg-eleball-primary px-4 py-2.5 text-sm font-medium text-white hover:bg-eleball-vs-primary-hover disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
        >
          {submitting ? <Loader2 className="w-4 h-4 animate-spin" /> : <Send className="w-4 h-4" />}
          <span className="hidden sm:inline">发送</span>
        </button>
      </div>

      {error && <p className="px-4 pb-3 text-xs text-eleball-vs-error">{error}</p>}
    </div>
  )
}
