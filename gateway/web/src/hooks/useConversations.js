import { useState, useCallback, useEffect } from 'react'
import { conversationApi } from '../api/client'

export function useConversations(userId) {
  const [conversations, setConversations] = useState([])
  const [currentConversation, setCurrentConversation] = useState(null)
  const [loading, setLoading] = useState(false)

  const loadConversations = useCallback(async () => {
    if (!userId) return
    setLoading(true)
    try {
      const res = await conversationApi.list()
      setConversations(res.items || [])
      localStorage.setItem('eleball_web_conversations', JSON.stringify(res.items || []))
    } catch (err) {
      console.warn('服务端拉取失败，使用本地缓存', err)
      const cached = localStorage.getItem('eleball_web_conversations')
      if (cached) {
        try {
          setConversations(JSON.parse(cached))
        } catch {
          setConversations([])
        }
      }
    } finally {
      setLoading(false)
    }
  }, [userId])

  const createConversation = useCallback(async ({ title, enableTools = false, model = '', provider = '' }) => {
    const conv = await conversationApi.create({ title, enable_tools: enableTools, model, provider })
    setConversations(prev => [conv, ...prev])
    setCurrentConversation(conv)
    return conv
  }, [])

  const updateConversation = useCallback(async (id, updates) => {
    await conversationApi.update(id, updates)
    setConversations(prev => prev.map(c => c.id === id ? { ...c, ...updates } : c))
    if (currentConversation?.id === id) {
      setCurrentConversation(prev => ({ ...prev, ...updates }))
    }
  }, [currentConversation])

  const switchConversation = useCallback(async (conv) => {
    setCurrentConversation(conv)
  }, [])

  const deleteConversation = useCallback(async (id) => {
    await conversationApi.delete(id)
    setConversations(prev => prev.filter(c => c.id !== id))
    if (currentConversation?.id === id) {
      setCurrentConversation(null)
    }
  }, [currentConversation])

  useEffect(() => {
    if (userId) {
      loadConversations()
    }
  }, [userId, loadConversations])

  return {
    conversations,
    currentConversation,
    loading,
    loadConversations,
    createConversation,
    updateConversation,
    switchConversation,
    deleteConversation,
    setCurrentConversation
  }
}
