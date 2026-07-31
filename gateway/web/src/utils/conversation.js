import { getJSON, setJSON, removeItem } from './storage'

const CONVERSATIONS_KEY = 'web_conversations'
const CURRENT_CONVERSATION_KEY = 'web_current_conversation_id'
const KEEP_THINKING_KEY = 'web_keep_thinking'
const FORK_LINKS_KEY = 'web_fork_links'

function conversationsKey(userId) {
  return userId ? `${CONVERSATIONS_KEY}_${userId}` : CONVERSATIONS_KEY
}

function currentConversationKey(userId) {
  return userId ? `${CURRENT_CONVERSATION_KEY}_${userId}` : CURRENT_CONVERSATION_KEY
}

/**
 * 加载对话历史，按 userId 隔离。
 * 若当前用户没有专属记录，回退到旧的无作用域 key 做迁移。
 */
export function loadConversations(userId = null) {
  const key = conversationsKey(userId)
  let list = getJSON(key, null)
  if (!Array.isArray(list) || list.length === 0) {
    if (userId) {
      list = getJSON(CONVERSATIONS_KEY, null)
    }
    if (!Array.isArray(list) || list.length === 0) {
      const first = createConversation()
      saveConversations([first], userId)
      return [first]
    }
  }
  return list
}

export function saveConversations(list, userId = null) {
  setJSON(conversationsKey(userId), list)
  // 一旦写入用户隔离数据，就删除旧的无作用域 key，避免其他账号继承
  if (userId) {
    removeItem(CONVERSATIONS_KEY)
  }
}

export function loadCurrentConversationId(userId = null) {
  let id = getJSON(currentConversationKey(userId), null)
  if (id === null && userId) {
    id = getJSON(CURRENT_CONVERSATION_KEY, null)
  }
  return id
}

export function saveCurrentConversationId(id, userId = null) {
  setJSON(currentConversationKey(userId), id)
  if (userId) {
    removeItem(CURRENT_CONVERSATION_KEY)
  }
}

export function createConversation(title = '新对话') {
  const now = Date.now()
  return {
    id: `conv_${now}_${Math.random().toString(36).slice(2, 7)}`,
    title,
    messages: [{ role: 'assistant', content: '你好，我是 Eleball。有什么可以帮你的吗？' }],
    enableWebSearch: false,
    searchProvider: 'baidu',
    model: '',
    provider: '',
    createdAt: now,
    updatedAt: now
  }
}

export function updateConversation(list, id, patch) {
  return list.map((c) =>
    c.id === id ? { ...c, ...patch, updatedAt: Date.now() } : c
  )
}

export function deleteConversation(list, id) {
  const next = list.filter((c) => c.id !== id)
  if (next.length === 0) {
    next.push(createConversation())
  }
  return next
}

export function generateTitle(firstUserMessage) {
  if (!firstUserMessage) return '新对话'
  const text = firstUserMessage.replace(/\s+/g, ' ').trim()
  if (!text) return '新对话'
  return text.length > 20 ? text.slice(0, 20) + '…' : text
}

function keepThinkingKey(userId) {
  return userId ? `${KEEP_THINKING_KEY}_${userId}` : KEEP_THINKING_KEY
}

/**
 * 读取是否保留思考过程，按 userId 隔离。
 */
export function loadKeepThinking(userId = null) {
  return getJSON(keepThinkingKey(userId), false)
}

/**
 * 保存是否保留思考过程，按 userId 隔离。
 */
export function saveKeepThinking(value, userId = null) {
  setJSON(keepThinkingKey(userId), !!value)
}

function forkLinksKey(userId) {
  return userId ? `${FORK_LINKS_KEY}_${userId}` : FORK_LINKS_KEY
}

/**
 * AR-12：读取会话分叉链接图，按 userId 隔离。
 * 结构 { [convId]: { parent: parentConvId, children: [childConvId, ...] } }
 */
export function loadForkLinks(userId = null) {
  return getJSON(forkLinksKey(userId), {}) || {}
}

/**
 * AR-12：保存会话分叉链接图，按 userId 隔离。
 */
export function saveForkLinks(value, userId = null) {
  setJSON(forkLinksKey(userId), value || {})
}
