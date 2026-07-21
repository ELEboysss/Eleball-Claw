import { useRef, useCallback } from 'react'
import { Image, Paperclip, X, FileText } from 'lucide-react'

export default function FileUploader({
  attachments = [],
  onChange,
  disabled = false,
  acceptImage = true
}) {
  const inputRef = useRef(null)

  const handleFileSelect = useCallback(
    async (e) => {
      const files = Array.from(e.target.files || [])
      if (files.length === 0) return
      for (const file of files) {
        // 非视觉模型下拒绝图片文件
        if (!acceptImage && isImageFile(file)) {
          onChange({ type: 'reject', reason: '当前模型不支持图片理解，请切换到视觉模型（VLM）后重试。' })
          continue
        }
        onChange({ type: 'add', file })
      }
      e.target.value = ''
    },
    [onChange, acceptImage]
  )

  const handlePaste = useCallback(
    (e) => {
      const items = Array.from(e.clipboardData?.items || [])
      let hasFile = false
      for (const item of items) {
        if (item.kind === 'file') {
          const file = item.getAsFile()
          if (file) {
            hasFile = true
            if (!acceptImage && isImageFile(file)) {
              onChange({ type: 'reject', reason: '当前模型不支持图片理解，请切换到视觉模型（VLM）后重试。' })
              continue
            }
            onChange({ type: 'add', file })
          }
        }
      }
      if (hasFile) {
        e.preventDefault()
      }
    },
    [onChange, acceptImage]
  )

  const handleRemove = useCallback(
    (id) => {
      onChange({ type: 'remove', id })
    },
    [onChange]
  )

  const imageAccept = acceptImage ? 'image/*' : undefined
  const fileAccept =
    '.txt,.md,.markdown,.json,.csv,.html,.css,.js,.jsx,.ts,.tsx,.py,.go,.java,.c,.cpp,.h,.xml,.yaml,.yml'
  const accept = imageAccept ? `${imageAccept},${fileAccept}` : fileAccept

  return (
    <div className="flex flex-col gap-2">
      {attachments.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {attachments.map((att) => (
            <div
              key={att.id}
              className="flex items-center gap-2 px-2 py-1.5 rounded-lg bg-eleball-primary-light/50 text-eleball-text text-xs border border-eleball-outline-variant"
            >
              {att.type === 'image' ? (
                <Image className="w-3.5 h-3.5 text-eleball-primary" />
              ) : (
                <FileText className="w-3.5 h-3.5 text-eleball-primary" />
              )}
              <span className="max-w-[120px] truncate">{att.name}</span>
              {att.type === 'image' && att.dataUrl && (
                <img
                  src={att.dataUrl}
                  alt={att.name}
                  className="w-6 h-6 rounded object-cover border border-eleball-outline"
                />
              )}
              <button
                onClick={() => handleRemove(att.id)}
                disabled={disabled}
                className="p-0.5 rounded hover:bg-eleball-primary/20 text-eleball-text-secondary disabled:opacity-50"
                title="移除"
              >
                <X className="w-3 h-3" />
              </button>
            </div>
          ))}
        </div>
      )}

      <div className="flex items-center gap-1">
        <input
          ref={inputRef}
          type="file"
          multiple
          accept={accept}
          onChange={handleFileSelect}
          disabled={disabled}
          className="hidden"
        />
        <button
          type="button"
          onClick={() => inputRef.current?.click()}
          disabled={disabled}
          className="p-2 rounded-full text-eleball-text-secondary hover:bg-eleball-primary-light hover:text-eleball-primary transition-colors disabled:opacity-50"
          title={acceptImage ? '上传图片或文件' : '上传文件（当前模型不支持图片）'}
        >
          <Paperclip className="w-4 h-4" />
        </button>
        {!acceptImage && (
          <span className="text-xs text-eleball-text-tertiary">当前模型不支持图片理解</span>
        )}
      </div>

      {/* 粘贴监听占位：父组件通过 onPaste 处理 */}
      <div className="sr-only" aria-hidden="true" onPaste={handlePaste} />
    </div>
  )
}

function isImageFile(file) {
  const imageTypes = ['image/png', 'image/jpeg', 'image/jpg', 'image/webp', 'image/gif']
  return imageTypes.includes(file.type) || /\.(png|jpe?g|webp|gif)$/i.test(file.name)
}
