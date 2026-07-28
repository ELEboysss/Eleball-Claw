import { useRef, useState } from 'react'
import { Upload, X, Loader2, ImageIcon } from 'lucide-react'
import { visualApi } from '../../api/client'

export default function ImageUploader({ value, onChange, label = '参考图（可选）', accept = 'image/png,image/jpeg,image/webp', maxSizeMB = 10 }) {
  const inputRef = useRef(null)
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')

  const handleFileChange = async (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    setError('')

    if (!accept.split(',').some((t) => file.type === t.trim())) {
      setError('仅支持 PNG、JPG、WebP 格式图片')
      return
    }
    if (file.size > maxSizeMB * 1024 * 1024) {
      setError(`图片大小不能超过 ${maxSizeMB} MB`)
      return
    }

    // 先本地预览
    const localUrl = URL.createObjectURL(file)
    onChange({ url: localUrl, file, status: 'uploading' })
    setUploading(true)

    try {
      const res = await visualApi.upload(file)
      onChange({ url: res.url, id: res.id, file, status: 'done' })
    } catch (err) {
      setError(err.message || '上传失败')
      onChange({ url: localUrl, file, status: 'error' })
    } finally {
      setUploading(false)
    }
  }

  const handleRemove = () => {
    onChange(null)
    setError('')
    if (inputRef.current) inputRef.current.value = ''
  }

  const handleClick = () => inputRef.current?.click()

  return (
    <div className="space-y-2">
      <label className="block text-sm font-medium text-eleball-vs-text-muted">{label}</label>
      {!value ? (
        <button
          type="button"
          onClick={handleClick}
          className="w-full flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed border-eleball-vs-border-variant bg-eleball-vs-surface-variant/50 px-4 py-6 text-sm text-eleball-vs-text-muted hover:border-eleball-primary hover:text-eleball-vs-accent transition-colors"
        >
          <Upload className="w-5 h-5" />
          <span>点击上传图片</span>
          <span className="text-xs text-eleball-vs-text-dim">PNG / JPG / WebP，最大 {maxSizeMB} MB</span>
        </button>
      ) : (
        <div className="relative rounded-lg overflow-hidden border border-eleball-vs-border-variant bg-eleball-vs-surface-variant">
          <img src={value.url} alt="参考图" className="w-full h-32 object-cover" />
          {value.status === 'uploading' && (
            <div className="absolute inset-0 flex items-center justify-center bg-eleball-vs-surface/70">
              <Loader2 className="w-6 h-6 animate-spin text-eleball-vs-accent" />
            </div>
          )}
          <button
            type="button"
            onClick={handleRemove}
            className="absolute top-1 right-1 p-1 rounded-md bg-eleball-vs-surface/80 text-eleball-vs-text-muted hover:bg-eleball-vs-error hover:text-white transition-colors"
          >
            <X className="w-4 h-4" />
          </button>
        </div>
      )}
      <input
        ref={inputRef}
        type="file"
        accept={accept}
        onChange={handleFileChange}
        className="hidden"
      />
      {error && <p className="text-xs text-eleball-vs-error">{error}</p>}
    </div>
  )
}
