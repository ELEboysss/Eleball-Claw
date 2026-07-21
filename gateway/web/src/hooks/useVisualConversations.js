import { useCallback, useEffect, useState } from 'react'
import { visualApi } from '../api/client'

export function useVisualConversations(mediaType = '') {
  const [conversations, setConversations] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  const load = useCallback(async () => {
    setLoading(true)
    setError(null)
    try {
      const data = await visualApi.listConversations(1, 100, mediaType)
      setConversations(data?.items || [])
    } catch (err) {
      setError(err)
    } finally {
      setLoading(false)
    }
  }, [mediaType])

  useEffect(() => {
    load()
  }, [load])

  const create = useCallback(async (title = '未命名视觉创作', type = mediaType || 'image') => {
    try {
      const conv = await visualApi.createConversation(title, type)
      setConversations((prev) => [conv, ...prev])
      return conv
    } catch (err) {
      setError(err)
      throw err
    }
  }, [mediaType])

  const remove = useCallback(async (id) => {
    try {
      await visualApi.deleteConversation(id)
      setConversations((prev) => prev.filter((c) => c.id !== id))
    } catch (err) {
      setError(err)
    }
  }, [])

  const update = useCallback(async (id, title) => {
    try {
      await visualApi.updateConversation(id, title)
      setConversations((prev) =>
        prev.map((c) => (c.id === id ? { ...c, title } : c))
      )
    } catch (err) {
      setError(err)
      throw err
    }
  }, [])

  return {
    conversations,
    loading,
    error,
    load,
    create,
    remove,
    update
  }
}
