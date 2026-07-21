import { useEffect, useState } from 'react'
import { modelApi } from '../api/client'

export function useVisualModels(mediaType) {
  const [models, setModels] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    modelApi
      .list()
      .then((data) => {
        if (cancelled) return
        const list = Array.isArray(data) ? data : data?.data || []
        const filtered = list.filter((m) => {
          if (mediaType === 'image') return m.supports_image
          if (mediaType === 'video') return m.supports_video
          return m.supports_image || m.supports_video
        }).map((m) => ({
          ...m,
          // 后端旧数据可能没有该字段，按协议兜底
          supports_image_input: m.supports_image_input ?? (m.protocol === 'agnes_image' || m.protocol === 'agnes_video' || m.protocol === 'seedance' || m.protocol === 'seedream'),
          supports_continuous_context: m.supports_continuous_context ?? false,
          // 视频时长配置：如果后端未配置，使用默认值
          video_min_duration: m.video_min_duration ?? 0,
          video_max_duration: m.video_max_duration ?? 0,
          video_duration_step: m.video_duration_step ?? 1
        }))
        setModels(filtered)
      })
      .catch((err) => setError(err))
      .finally(() => setLoading(false))
    return () => {
      cancelled = true
    }
  }, [mediaType])

  const imageModels = models.filter((m) => m.supports_image)
  const videoModels = models.filter((m) => m.supports_video)

  // 组件内统一使用 model_name 作为 model 值，provider 单独透传
  return { models, imageModels, videoModels, loading, error }
}