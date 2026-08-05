import { createContext, useContext, useState, useEffect, useMemo, useCallback } from 'react'
import { useAuth } from './AuthContext'
import { conversationApi } from '../api/client'
import {
  loadConversations as loadConversationsFromStorage,
  saveConversations as saveConversationsToStorage,
  loadCurrentConversationId as loadCurrentIdFromStorage,
  saveCurrentConversationId as saveCurrentIdToStorage,
  createConversation
} from '../utils/conversation'

const ChatContext = createContext(null)

// 将后端消息对象映射为前端消息对象
function mapBackendMessage(m) {
  let toolSummary = ''
  let toolSteps = []
  if (m.tool_results) {
    try {
      const parsed = JSON.parse(m.tool_results)
      toolSummary = parsed.summary || ''
      toolSteps = parsed.steps || []
    } catch (e) {
      toolSummary = ''
      toolSteps = []
    }
  }
  return {
    id: m.id,
    role: m.role,
    content: m.content,
    clientMessageId: m.client_message_id,
    reasoningContent: m.reasoning_content || '',
    toolSummary,
    toolSteps,
    createdAt: m.created_at
  }
}

// 判定是否为流式期间插入的空 assistant 占位消息（无正文/思考/步骤/工具/中间输出）。
// 这类占位仅存在于本地 state 与 localStorage，后端从未持久化；流式中断（如渲染崩溃）后
// 会常驻本地历史，并在后续回合被 mergeMessages 当作本地独有保留，造成「空回复 + 结果回复」。
function isEmptyAssistantPlaceholder(m) {
  if (m?.role !== 'assistant') return false
  return (
    !m.content &&
    !m.reasoningContent &&
    !m.intermediateContent &&
    !(m.steps && m.steps.length) &&
    !(m.toolSteps && m.toolSteps.length)
  )
}

// 合并本地消息与后端消息：请求失败或后端数据不完整时，保留本地已有的助手回复。
// 若后端消息更完整（数量不少于本地），直接采用后端数据；否则保留本地并追加后端独有的消息。
// 本地空 assistant 占位一律剔除，避免中断轮的空壳常驻历史。
function mergeMessages(localMessages = [], backendMessages = []) {
  const local = (Array.isArray(localMessages) ? localMessages : []).filter(
    (m) => !isEmptyAssistantPlaceholder(m)
  )
  if (!Array.isArray(backendMessages) || backendMessages.length === 0) {
    return local
  }
  if (local.length === 0) {
    return backendMessages
  }
  // 后端消息不少于本地时，认为后端已完整持久化，直接采用
  if (backendMessages.length >= local.length) {
    return backendMessages
  }
  // 否则以本地为基准，追加后端可能独有的消息（按 role + 内容前 120 字符去重）
  const makeKey = (m) => `${m.role || ''}:${(m.content || '').slice(0, 120)}`
  const seen = new Set(local.map(makeKey))
  const merged = [...local]
  for (const m of backendMessages) {
    const key = makeKey(m)
    if (!seen.has(key)) {
      merged.push(m)
      seen.add(key)
    }
  }
  return merged
}

// 合并标题：后端非默认标题优先；后端为默认标题时，保留本地已有的生成标题。
// 防止标题同步失败或刷新过早导致后端默认“新对话”覆盖本地生成标题。
function mergeTitle(backendTitle, localTitle) {
  const isBackendDefault = !backendTitle || backendTitle === '新对话'
  const isLocalDefault = !localTitle || localTitle === '新对话'
  if (!isBackendDefault) return backendTitle
  if (!isLocalDefault) return localTitle
  return backendTitle || localTitle || '新对话'
}

export function ChatProvider({ children }) {
  const { user } = useAuth()
  const userId = user?.user_id

  const [conversations, setConversations] = useState([])
  const [currentConversationId, setCurrentConversationId] = useState(null)
  const [initialized, setInitialized] = useState(false)

  // 用户切换时从后端拉取对话列表与消息，并与本地缓存合并，避免请求失败覆盖本地历史。
  useEffect(() => {
    if (!userId) return

    let cancelled = false

    const load = async () => {
      // 先读取本地缓存作为合并基础，任何后端失败都可以回退到这份数据
      const local = loadConversationsFromStorage(userId)
      const localMap = new Map(local.map((c) => [c.id, c]))

      try {
        const res = await conversationApi.list()
        const items = res?.items || []
        const backendConvs = items.map((conv) => {
          const existing = localMap.get(conv.id)
          return {
            id: conv.id,
            title: mergeTitle(conv.title, existing?.title),
            // 先复用本地消息，避免拉取消息前出现空白或默认 welcome 闪烁
            messages: existing?.messages || [],
            enableTools: !!conv.enable_tools,
            enableWebSearch: !!conv.enable_web_search,
            searchProvider: conv.search_provider || 'baidu',
            // 会话绑定的助手（空字符串 = 未绑定，使用全部已激活工具）
            assistantId: conv.assistant_id || '',
            // 会话所属的对话分组（空字符串 = 未分组）
            teamId: conv.team_id || '',
            // 会话绑定的模型配置（model 身份），切换对话时据此恢复 currentProfileId
            model: conv.model || '',
            provider: conv.provider || '',
            // AR-23/AR-27：会话持久化的工作目录（claw 本地）。重启后从后端恢复，避免 cwd 丢失
            cwd: conv.cwd || '',
            createdAt: conv.created_at,
            updatedAt: conv.updated_at
          }
        })

        if (backendConvs.length === 0) {
          // 后端没有对话时，保留本地已有对话；本地也没有才创建 welcome
          const welcome = createConversation()
          const next = local.length > 0 ? local : [welcome]
          if (!cancelled) {
            setConversations(next)
            setCurrentConversationId(loadCurrentIdFromStorage(userId) || next[0]?.id)
            setInitialized(true)
          }
          return
        }

        // 合并后端对话与本地对话：后端更新元数据，本地独有的对话保留
        const backendIds = new Set(backendConvs.map((c) => c.id))
        const mergedConvs = [
          ...backendConvs,
          ...local.filter((c) => !backendIds.has(c.id))
        ]

        if (!cancelled) {
          setConversations(mergedConvs)
          setCurrentConversationId(loadCurrentIdFromStorage(userId) || mergedConvs[0]?.id)
        }

        // 异步拉取每个后端对话的消息，失败时保留本地消息
        const messagesResults = await Promise.all(
          backendConvs.map(async (conv) => {
            const localConv = localMap.get(conv.id)
            try {
              const msgRes = await conversationApi.listMessages(conv.id)
              const msgItems = msgRes?.items || []
              const backendMessages = msgItems.map(mapBackendMessage)
              return {
                id: conv.id,
                messages: mergeMessages(localConv?.messages, backendMessages)
              }
            } catch (e) {
              console.warn(`拉取对话 ${conv.id} 消息失败，使用本地缓存`, e)
              return { id: conv.id, messages: localConv?.messages || [] }
            }
          })
        )

        if (!cancelled) {
          setConversations((prev) =>
            prev.map((c) => {
              const found = messagesResults.find((r) => r.id === c.id)
              if (!found) return c
              // 后端和本地都没有消息时留空，由 Chat 空状态 UI 呈现欢迎语（不持久化、不发 LLM）
              const messages = found.messages?.length
                ? found.messages
                : c.messages?.length
                  ? c.messages
                  : []
              return { ...c, messages }
            })
          )
          setInitialized(true)
        }
      } catch (e) {
        // 后端完全不可用，回退到本地缓存
        console.error('从后端加载对话失败:', e)
        if (!cancelled) {
          setConversations(local)
          setCurrentConversationId(loadCurrentIdFromStorage(userId) || local[0]?.id)
          setInitialized(true)
        }
      }
    }

    load()
    return () => { cancelled = true }
  }, [userId])

  // 对话列表变更后持久化到本地，作为元数据缓存
  useEffect(() => {
    if (!initialized || !userId) return
    saveConversationsToStorage(conversations, userId)
  }, [conversations, userId, initialized])

  // 当前对话 ID 变更后持久化
  useEffect(() => {
    if (!initialized) return
    saveCurrentIdToStorage(currentConversationId, userId)
  }, [currentConversationId, userId, initialized])

  const currentConversation = useMemo(
    () => conversations.find((c) => c.id === currentConversationId) || conversations[0],
    [conversations, currentConversationId]
  )

  const refreshMessages = useCallback(
    async (conversationId) => {
      if (!conversationId) return
      try {
        const res = await conversationApi.listMessages(conversationId)
        const items = res?.items || []
        const messages = items.map((m) => {
          let toolSummary = ''
          let toolSteps = []
          if (m.tool_results) {
            try {
              const parsed = JSON.parse(m.tool_results)
              toolSummary = parsed.summary || ''
              toolSteps = parsed.steps || []
            } catch (e) {
              toolSummary = ''
              toolSteps = []
            }
          }
          return {
            id: m.id,
            role: m.role,
            content: m.content,
            reasoningContent: m.reasoning_content || '',
            toolSummary,
            toolSteps,
            createdAt: m.created_at
          }
        })
        setConversations((prev) =>
          prev.map((c) => (c.id === conversationId ? { ...c, messages } : c))
        )
      } catch (e) {
        console.error('刷新消息失败:', e)
      }
    },
    []
  )

  const value = useMemo(
    () => ({
      conversations,
      setConversations,
      currentConversation,
      currentConversationId,
      setCurrentConversationId,
      refreshMessages,
      initialized
    }),
    [conversations, currentConversation, currentConversationId, refreshMessages, initialized]
  )

  return <ChatContext.Provider value={value}>{children}</ChatContext.Provider>
}

export function useChat() {
  const ctx = useContext(ChatContext)
  if (!ctx) {
    throw new Error('useChat must be used within ChatProvider')
  }
  return ctx
}
