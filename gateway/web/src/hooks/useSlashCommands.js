import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { slashApi } from '../api/client'

/**
 * 管理 slash 命令列表：首次触发 `/` 时 lazy load，前端本地 fuzzy 过滤。
 * @param {boolean} enabled 是否启用（已登录）
 */
export function useSlashCommands(enabled) {
  const [categories, setCategories] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const loadedRef = useRef(false)

  const load = useCallback(async () => {
    if (loadedRef.current || !enabled) return
    loadedRef.current = true
    setLoading(true)
    setError('')
    try {
      const data = await slashApi.listCommands()
      const list = data?.categories || data || []
      setCategories(Array.isArray(list) ? list : [])
    } catch (err) {
      setError(err.message || '加载命令失败')
      loadedRef.current = false
    } finally {
      setLoading(false)
    }
  }, [enabled])

  useEffect(() => {
    if (enabled && !loadedRef.current) {
      load()
    }
  }, [enabled, load])

  /**
   * 按 query 过滤命令；空 query 返回全部。
   * @param {string} query 去掉 `/` 后的关键字
   */
  const filter = useCallback((query) => {
    const q = query.trim().toLowerCase()
    if (!q) return categories
    return categories
      .map((cat) => ({
        ...cat,
        commands: (cat.commands || []).filter((cmd) => {
          const hay = `${cmd.name} ${cmd.description || ''} ${cmd.arguments_hint || ''}`.toLowerCase()
          return hay.includes(q)
        })
      }))
      .filter((cat) => cat.commands.length > 0)
  }, [categories])

  return useMemo(() => ({
    categories,
    loading,
    error,
    load,
    filter
  }), [categories, loading, error, load, filter])
}
