import { useState, useEffect } from 'react'
import { X, Smartphone, Apple } from 'lucide-react'
import QRCode from 'qrcode'

export default function DownloadModal({ open, onClose }) {
  const [activeTab, setActiveTab] = useState('mobile')
  const [qrUrl, setQrUrl] = useState('')
  const [version, setVersion] = useState('')
  const [downloadUrl, setDownloadUrl] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return

    async function loadManifest() {
      try {
        // 公开接口，无需 JWT；/api 为网关 API 前缀
        const res = await fetch('/v1/releases/android', { cache: 'no-cache' })
        const body = await res.json()
        if (body.code !== 0 || !body.data) {
          throw new Error(body.message || '版本清单加载失败')
        }

        const manifest = body.data
        const channel = manifest.defaultChannel || Object.keys(manifest.current || {})[0]
        const version = manifest.current?.[channel]
        const info = manifest.versions?.[version]
        if (!info) {
          throw new Error('未找到可用版本')
        }

        const downloadUrl = `${window.location.origin}/api/releases/android/download?version=${encodeURIComponent(info.version)}`
        const dataUrl = await QRCode.toDataURL(downloadUrl, {
          width: 176,
          margin: 2,
          color: { dark: '#21005D', light: '#FFFFFF' },
          errorCorrectionLevel: 'M'
        })

        setQrUrl(dataUrl)
        setVersion(info.version)
        setDownloadUrl(downloadUrl)
        setError('')
      } catch (err) {
        console.error('加载下载二维码失败：', err)
        setError(err.message || '加载失败')
        setQrUrl('')
      } finally {
        setLoading(false)
      }
    }

    setLoading(true)
    loadManifest()
  }, [open])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center px-4 bg-black/40 backdrop-blur-sm">
      <div className="relative w-full max-w-sm dialog-panel p-6">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 p-1 rounded-full text-eleball-text-tertiary hover:bg-eleball-surface-variant"
        >
          <X className="w-5 h-5" />
        </button>

        <h2 className="text-xl font-bold text-eleball-text mb-6">获取应用程序</h2>

        {/* Tabs */}
        <div className="flex p-1 bg-eleball-surface-variant rounded-2xl mb-6">
          <button
            onClick={() => setActiveTab('mobile')}
            className={`flex-1 flex items-center justify-center gap-2 py-2 rounded-xl text-sm font-semibold transition-colors ${
              activeTab === 'mobile'
                ? 'bg-eleball-primary text-white'
                : 'text-eleball-text-secondary hover:text-eleball-text'
            }`}
          >
            <Smartphone className="w-4 h-4" />
            Android
          </button>
          <button
            disabled
            className="flex-1 flex items-center justify-center gap-2 py-2 rounded-xl text-sm font-semibold text-eleball-text-tertiary cursor-not-allowed"
          >
            <Apple className="w-4 h-4" />
            iOS（未上线）
          </button>
        </div>

        {/* Content */}
        <div className="flex flex-col items-center min-h-[11rem]">
          {activeTab === 'mobile' ? (
            loading ? (
              <div className="w-44 h-44 rounded-xl bg-eleball-surface-variant animate-pulse" />
            ) : error ? (
              <p className="text-sm text-red-500 text-center">{error}</p>
            ) : (
              <>
                <div className="w-44 h-44 bg-white p-3 rounded-xl border border-eleball-outline-variant">
                  {qrUrl ? (
                    <img
                      src={qrUrl}
                      alt="扫码下载 Android 应用"
                      className="w-full h-full"
                    />
                  ) : null}
                </div>
                <p className="mt-4 text-sm text-eleball-text-secondary">
                  扫码下载 Android 应用
                  {version && downloadUrl ? (
                    <a
                      href={downloadUrl}
                      className="ml-1 text-eleball-primary underline hover:opacity-80"
                      download
                    >
                      v{version}
                    </a>
                  ) : null}
                </p>
              </>
            )
          ) : (
            <p className="text-sm text-eleball-text-secondary">桌面应用即将上线</p>
          )}
        </div>
      </div>
    </div>
  )
}
