import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import useSEO from '../hooks/useSEO'
import { useNavigate } from 'react-router-dom'
import {
  Send,
  MoreHorizontal,
  Square,
  Bot,
  User as UserIcon,
  AlertCircle,
  Settings,
  ChevronDown,
  ChevronRight,
  Menu,
  Plus,
  Copy,
  Check,
  Trash2,
  X,
  Image,
  Film,
  FileText,
  Download,
  Globe,
  Wrench,
  Folders,
  FolderInput,
  GitFork,
  Folder,
  PanelRight
} from 'lucide-react'
import { useAuth } from '../context/AuthContext'
import { useChat } from '../context/ChatContext'
import { modelApi, billingApi, eleAgentApi, conversationApi, agentApi, assistantApi, teamApi, clawFilesApi } from '../api/client'
import { streamChat } from '../utils/sse'
import LoginModal from '../components/LoginModal'
import ModelSettings from '../components/ModelSettings'
import AssistantPicker from '../components/AssistantPicker'
import TeamModal from '../components/TeamModal'
import AgentSwitch from '../components/AgentSwitch'
import AgentStream from '../components/AgentStream'
import AgentSteps from '../components/AgentSteps'
import AgentSessionList from '../components/AgentSessionList'
import ConfirmDialog from '../components/ConfirmDialog'
import BranchNavigator from '../components/BranchNavigator'
import DirectoryPicker from '../components/DirectoryPicker'
import FileExplorer from '../components/FileExplorer'
import FileViewer from '../components/FileViewer'
import { useAgent } from '../hooks/useAgent'
import { Link } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import remarkBreaks from 'remark-breaks'
import { getItem } from '../utils/storage'
import {
  loadProfiles,
  saveProfiles,
  loadCurrentProfileId,
  saveCurrentProfileId,
  resolveBaseUrl,
  parseEleAgentModelName,
  loadCachedCredentials,
  saveCachedCredentials,
  clearCachedCredentials,
  PROVIDERS
} from '../utils/model'
import {
  createConversation,
  deleteConversation,
  generateTitle,
  loadKeepThinking,
  saveKeepThinking,
  loadForkLinks,
  saveForkLinks
} from '../utils/conversation'
import {
  fileToContentPart,
  buildMessageContent,
  contentToText,
  downloadTextFile,
  safeParseJSON
} from '../utils/file'

// 仅保留支持文字对话的模型（对话页模型选择器）；
// supports_chat 缺省视为支持，兼容未返回该字段的旧后端/旧数据。
// 纯图片/纯视频生成模型由视觉工作室按 supports_image / supports_video 单独消费。
const filterChatCapableModels = (list) => list.filter((m) => m?.supports_chat !== false)

export default function Chat() {
  useSEO('在线 AI 对话', '多轮对话、多模型切换、语音输入。登录即享每日免费额度。')
  const navigate = useNavigate()
  const { isLoggedIn, token, user } = useAuth()
  const {
    conversations,
    setConversations,
    currentConversation,
    currentConversationId,
    setCurrentConversationId,
    refreshMessages,
    initialized
  } = useChat()
  const [input, setInput] = useState('')
  const [attachments, setAttachments] = useState([])
  const [loading, setLoading] = useState(false)
  // AR-05-O5：替代原生 confirm() 的确认弹窗状态
  const [confirmState, setConfirmState] = useState({ open: false })
  const [models, setModels] = useState([])
  const [balance, setBalance] = useState(null)
  const [lastUsage, setLastUsage] = useState(null) // AR-07：最近一次 Agent 执行用量（tokens/cost/步数/上下文规模）
  const [loginOpen, setLoginOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [sidebarOpen, setSidebarOpen] = useState(false)
  const [error, setError] = useState('')
  const [enableTools, setEnableTools] = useState(false)
  const [enableWebSearch, setEnableWebSearch] = useState(false)
  const [searchProvider, setSearchProvider] = useState('baidu')
  const [availableSearchProviders, setAvailableSearchProviders] = useState([])
  // 会话绑定的助手（'' = 默认，全部已激活工具）与我的助手列表
  const [assistantId, setAssistantId] = useState('')
  const [assistants, setAssistants] = useState([])
  // AR-11：本地工作目录（cwd）+ 文件浏览器/预览侧栏
  const [cwd, setCwd] = useState('')
  const [cwdPickerOpen, setCwdPickerOpen] = useState(false)
  const [filePanelOpen, setFilePanelOpen] = useState(false)
  const [selectedFile, setSelectedFile] = useState(null) // 相对 cwd 的路径
  const [gitStatus, setGitStatus] = useState(null)
  const [fileRefreshKey, setFileRefreshKey] = useState(0)
  // 助手切换弹窗：以独立按钮触发、弹窗内切换，避免下拉浮层被输入框 overflow-hidden 裁切
  const [assistantPickerOpen, setAssistantPickerOpen] = useState(false)
  const [keepThinking, setKeepThinking] = useState(() => loadKeepThinking(user?.user_id))
  // AR-13 O12：< sm 折叠次级工具开关到「更多」
  const [moreToolsOpen, setMoreToolsOpen] = useState(false)
  // AR-12 会话分叉：分支链接图 { [convId]: { parent, children[] } }，持久化按 userId 隔离
  const [forkLinks, setForkLinks] = useState(() => loadForkLinks(user?.user_id))
  const [forkingMessageId, setForkingMessageId] = useState(null) // AR-12：分叉中状态（禁用按钮 + 指示）
  const [agentSessionRefresh, setAgentSessionRefresh] = useState(0)
  // 对话分组（Agent Team）：侧栏分组展示与「移动到组」用
  const [teams, setTeams] = useState([])
  const [teamModalOpen, setTeamModalOpen] = useState(false)
  // 移动到组下拉：记录当前展开归属选择的对话 ID
  const [moveMenuConvId, setMoveMenuConvId] = useState(null)
  // 当前正在流式更新的 Agent assistant 消息 ID，用于把 steps 实时写入对话消息
  const [streamingAgentMsgId, setStreamingAgentMsgId] = useState(null)
  const messagesEndRef = useRef(null)
  const abortRef = useRef(false)
  const fileInputRef = useRef(null)
  const {
    execute: executeAgent,
    reset: resetAgent,
    status: agentStatus,
    toolSteps,
    currentStep: agentCurrentStep,
    answer: agentAnswer,
    reasoningContent: agentReasoningContent,
    intermediateAnswer: agentIntermediateAnswer,
    resources: agentResources,
    error: agentError,
    warning: agentWarning,
    steps: agentSteps,
    abort: abortAgent
  } = useAgent()

  // 模型 Profile 状态（与 App 端 ModelProfile 对齐），按当前登录用户隔离
  const [profiles, setProfiles] = useState(() => loadProfiles(user?.default_model_profile, user?.user_id))
  const [currentProfileId, setCurrentProfileId] = useState(() => {
    const id = loadCurrentProfileId(user?.user_id)
    const list = loadProfiles(user?.default_model_profile, user?.user_id)
    return list.find((p) => p.id === id)?.id || list.find((p) => p.isDefault)?.id || list[0]?.id
  })

  const currentProfile = profiles.find((p) => p.id === currentProfileId) || profiles[0]

  // 如果当前选中的 Profile 被删除或不存在，回退到默认/第一个
  // 直接 setCurrentProfileId（不走 switchProfile），避免回退时把模型写回后端
  useEffect(() => {
    if (profiles.length === 0) return
    if (!profiles.some((p) => p.id === currentProfileId)) {
      const fallback = profiles.find((p) => p.isDefault)?.id || profiles[0].id
      setCurrentProfileId(fallback)
    }
  }, [profiles, currentProfileId])

  // 登录后拿到 default_model_profile，同步更新默认 Ele Agent 配置
  useEffect(() => {
    if (!user?.default_model_profile) return
    const d = user.default_model_profile
    const next = profiles.map((p) =>
      p.provider === 'ELE_AGENT' && p.id === 'eleagent_default'
        ? { ...p, modelName: d.model_name, systemPrompt: d.system_prompt }
        : p
    )
    if (JSON.stringify(next) !== JSON.stringify(profiles)) {
      updateProfiles(next)
    }
  }, [user?.default_model_profile])

  // 切换账户时，重新加载该用户隔离的数据，并清除旧账户的 Ele Agent 凭证缓存
  useEffect(() => {
    if (!user?.user_id) return
    const nextProfiles = loadProfiles(user.default_model_profile, user.user_id)
    setProfiles(nextProfiles)
    const savedProfileId = loadCurrentProfileId(user.user_id)
    const nextProfileId =
      (savedProfileId && nextProfiles.some((p) => p.id === savedProfileId) ? savedProfileId : null) ||
      nextProfiles.find((p) => p.isDefault)?.id ||
      nextProfiles[0]?.id
    setCurrentProfileId(nextProfileId)
    setKeepThinking(loadKeepThinking(user.user_id))
    setForkLinks(loadForkLinks(user.user_id))

    // 强制重新获取 Ele Agent 凭证，避免使用旧账户缓存
    nextProfiles.filter((p) => p.provider === 'ELE_AGENT').forEach((p) => clearCachedCredentials(p.id, user.user_id))
  }, [user?.user_id])

  // 保留思考过程开关变更后持久化
  useEffect(() => {
    saveKeepThinking(keepThinking, user?.user_id)
  }, [keepThinking, user?.user_id])

  // AR-12：分叉链接图变更后持久化
  useEffect(() => {
    saveForkLinks(forkLinks, user?.user_id)
  }, [forkLinks, user?.user_id])

  // 拉取 Ele Agent 模型列表与余额；账户切换时也会重新拉取
  useEffect(() => {
    if (!isLoggedIn || !user?.user_id) return
    let cancelled = false
    Promise.all([
      modelApi.list().catch(() => []),
      billingApi.getBalance().catch(() => null)
    ]).then(([modelsData, balanceData]) => {
      if (cancelled) return
      const list = filterChatCapableModels(Array.isArray(modelsData) ? modelsData : [])
      setModels(list)

      // 如果当前默认 Ele Agent 配置的模型不在后端列表中，用第一个可用项兜底
      const activeProfile = profiles.find((p) => p.id === currentProfileId)
      if (list.length > 0 && activeProfile?.provider === 'ELE_AGENT') {
        const fullName = (m) => `${m.provider}/${m.model_name}`
        const exists = list.some((m) => fullName(m) === activeProfile.modelName)
        if (!exists) {
          const next = list[0]
          updateProfile(activeProfile.id, { modelName: fullName(next), name: next.display_name || next.model_name })
        }
      }
      setBalance(balanceData)
    })
    return () => { cancelled = true }
  }, [isLoggedIn, currentProfileId, user?.user_id])

  // 用户切回聊天页时重新拉取模型选项，确保 admin 中修改的 supports_vision 等配置及时生效
  useEffect(() => {
    if (!isLoggedIn || !user?.user_id) return
    const refreshModels = () => {
      if (document.visibilityState !== 'visible') return
      modelApi.list()
        .then((data) => setModels(filterChatCapableModels(Array.isArray(data) ? data : [])))
        .catch(() => {})
    }
    document.addEventListener('visibilitychange', refreshModels)
    window.addEventListener('focus', refreshModels)
    return () => {
      document.removeEventListener('visibilitychange', refreshModels)
      window.removeEventListener('focus', refreshModels)
    }
  }, [isLoggedIn, user?.user_id])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [currentConversation?.messages])

  // 切换对话时，恢复该对话上次保存的错误提示
  useEffect(() => {
    setError(currentConversation?.error || '')
  }, [currentConversationId])

  // BYOK 模型不支持 Agent 工具，切换模型时自动关闭
  const supportsAgent = currentProfile?.provider === 'ELE_AGENT'
  // AR-07：token 数值紧凑格式化（1234 -> 1.2k，1234567 -> 1.2M）
  const formatTokens = (n) => {
    if (n == null) return ''
    if (n >= 1000000) return (n / 1000000).toFixed(1).replace(/\.0$/, '') + 'M'
    if (n >= 1000) return (n / 1000).toFixed(1).replace(/\.0$/, '') + 'k'
    return String(n)
  }
  // AR-05：aria-live 执行状态播报文本（思考中/调用工具/生成回答/出错），供屏幕阅读器感知 Agent 执行进度
  const statusAnnouncement = agentStatus === 'error'
    ? '生成出错'
    : !loading
      ? ''
      : agentStatus === 'executing'
        ? '正在调用工具'
        : agentStatus === 'answering'
          ? '正在生成回答'
          : '正在思考'
  useEffect(() => {
    if (!supportsAgent && enableTools) {
      setEnableTools(false)
      setEnableWebSearch(false)
    }
  }, [supportsAgent, enableTools])

  // 拉取当前已配置的可用搜索源列表，未配置 key 的源不展示
  useEffect(() => {
    if (!isLoggedIn) return
    agentApi.listSearchProviders()
      .then((data) => {
        const list = Array.isArray(data) ? data : []
        setAvailableSearchProviders(list)
        // 若当前选中的源已不可用，自动切换到第一个可用源
        if (list.length > 0 && !list.some((p) => p.name === searchProvider)) {
          setSearchProvider(list[0].name)
        }
      })
      .catch(() => {
        setAvailableSearchProviders([])
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isLoggedIn])

  // 拉取我的助手列表；原生 select 无法监听「展开」，改为切回页面时刷新，
  // 保证在「我的助手」新建/删除后回到对话页能及时同步
  useEffect(() => {
    if (!isLoggedIn) return
    const load = () =>
      assistantApi
        .list()
        .then((d) => setAssistants(Array.isArray(d) ? d : d?.items || []))
        .catch(() => {})
    load()
    const onFocus = () => {
      if (document.visibilityState === 'visible') load()
    }
    document.addEventListener('visibilitychange', onFocus)
    window.addEventListener('focus', onFocus)
    return () => {
      document.removeEventListener('visibilitychange', onFocus)
      window.removeEventListener('focus', onFocus)
    }
  }, [isLoggedIn])

  // 拉取对话分组列表（与助手列表同节奏：切回页面时刷新，保证分组管理后及时同步）
  const loadTeams = useCallback(() => {
    if (!isLoggedIn) return
    teamApi
      .list()
      .then((d) => setTeams(Array.isArray(d) ? d : d?.items || []))
      .catch(() => {})
  }, [isLoggedIn])

  useEffect(() => {
    loadTeams()
    const onFocus = () => {
      if (document.visibilityState === 'visible') loadTeams()
    }
    document.addEventListener('visibilitychange', onFocus)
    window.addEventListener('focus', onFocus)
    return () => {
      document.removeEventListener('visibilitychange', onFocus)
      window.removeEventListener('focus', onFocus)
    }
  }, [loadTeams])

  // 侧栏分组：按 team_id 分桶，未分组单列一组（key ''）
  const groupedConversations = useMemo(() => {
    const buckets = new Map()
    for (const conv of conversations) {
      const key = conv.teamId || ''
      if (!buckets.has(key)) buckets.set(key, [])
      buckets.get(key).push(conv)
    }
    return buckets
  }, [conversations])

  // 移动对话到组：PATCH team_id（空串=移出分组）并就地更新本地会话的 teamId。
  // 新建后尚未产生消息的对话后端尚无记录（PATCH 返回「对话不存在」），
  // 此时无法归组，提示用户先发送消息落库后再试。
  const handleMoveConversation = async (convId, teamId) => {
    setMoveMenuConvId(null)
    const target = teamId || ''
    try {
      await conversationApi.update(convId, { team_id: target })
    } catch (err) {
      const msg = err.message || ''
      const isNotFound = /不存在|not found|404/i.test(msg)
      if (isNotFound) {
        setError('对话未落库，请发送消息后再试')
      } else {
        console.error('移动分组失败:', err)
        setError('移动分组失败')
      }
      return
    }
    setConversations((prev) =>
      prev.map((c) => (c.id === convId ? { ...c, teamId: target } : c))
    )
    loadTeams()
  }

  // 切换对话时恢复该对话的 Agent / 联网 / 搜索源 / 助手 / 模型设置
  useEffect(() => {
    if (!currentConversation) return
    setEnableTools(!!currentConversation.enableTools)
    setEnableWebSearch(!!currentConversation.enableWebSearch)
    const savedProvider = currentConversation.searchProvider || 'baidu'
    const exists = availableSearchProviders.some((p) => p.name === savedProvider)
    setSearchProvider(exists ? savedProvider : (availableSearchProviders[0]?.name || 'baidu'))
    setAssistantId(currentConversation.assistantId || '')
    // 恢复该对话绑定的模型：按 model+provider 找匹配 profile（直接 setState，不触发后端写）
    if (currentConversation.model && currentConversation.provider) {
      const matched = profiles.find(
        (p) => p.provider === currentConversation.provider && p.modelName === currentConversation.model
      )
      if (matched && matched.id !== currentProfileId) {
        setCurrentProfileId(matched.id)
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentConversationId, availableSearchProviders])

  // Agent / 联网 / 搜索源 / 助手设置变化时同步到本地 conversation 和后端
  useEffect(() => {
    if (!currentConversation || !isLoggedIn) return
    const needsUpdate =
      !!currentConversation.enableTools !== enableTools ||
      !!currentConversation.enableWebSearch !== enableWebSearch ||
      (currentConversation.searchProvider || 'baidu') !== searchProvider ||
      (currentConversation.assistantId || '') !== assistantId
    if (!needsUpdate) return

    updateConversations((prev) =>
      prev.map((c) =>
        c.id === currentConversation.id
          ? { ...c, enableTools, enableWebSearch, searchProvider, assistantId, updatedAt: Date.now() }
          : c
      )
    )
    conversationApi
      .update(currentConversation.id, {
        enable_tools: enableTools,
        enable_web_search: enableWebSearch,
        search_provider: searchProvider,
        // 空字符串 = 清除会话绑定的助手
        assistant_id: assistantId
      })
      .catch(() => {})
  }, [enableTools, enableWebSearch, searchProvider, assistantId, currentConversation?.id, isLoggedIn])

  // AR-11：cwd 变化或手动刷新时拉取 Git 状态，供 FileExplorer 色标
  useEffect(() => {
    if (!cwd) { setGitStatus(null); return }
    let cancelled = false
    clawFilesApi.gitStatus(cwd)
      .then((d) => { if (!cancelled) setGitStatus(d) })
      .catch(() => { if (!cancelled) setGitStatus(null) })
    return () => { cancelled = true }
  }, [cwd, fileRefreshKey])

  const updateConversations = (next) => {
    setConversations(next)
  }

  // 将消息持久化到后端 ChatMessage 表，失败时不阻塞 UI。
  // 返回后端自动生成的对话标题（保存第一条用户消息时可能生成），便于前端立即同步。
  const persistMessage = useCallback(async (conversationId, message) => {
    if (!conversationId || !message) return null
    try {
      const payload = {
        role: message.role,
        content: message.content || '',
        client_message_id: message.clientMessageId || `${message.role}_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
      }
      if (message.reasoningContent) {
        payload.reasoning_content = message.reasoningContent
      }
      if (message.toolSummary || (message.toolSteps && message.toolSteps.length > 0)) {
        payload.tool_results = JSON.stringify({
          summary: message.toolSummary || '',
          steps: message.toolSteps || []
        })
      }
      const res = await conversationApi.saveMessage(conversationId, payload)
      // AR-12：对齐服务端消息 ID（服务端生成 msg_xxx），供分叉 entry_id 解析；
      // 本地占位 id（如 agent_xxx）过长且与服务端不一致，这里以服务端返回为准回写。
      if (res?.message?.id) {
        message.id = res.message.id
      }
      return res?.title || null
    } catch (err) {
      console.error('保存消息失败:', err)
      return null
    }
  }, [])


  const switchConversation = (id) => {
    setCurrentConversationId(id)
    setSidebarOpen(false)
  }

  // 点击 Agent 关键节点时，跳回到该 Session 所属的对话。
  // 若该对话已不存在（例如被用户删除），不再新建兜底对话，避免产生无法删除的本地幽灵对话。
  const handleSelectSession = (session) => {
    if (!session?.conversation_id) return
    const conv = conversations.find((c) => c.id === session.conversation_id)
    if (!conv) return
    switchConversation(conv.id)
  }

  // AR-12：从分叉点消息复制父 session 对话历史到新 session，切换到新分叉对话继续探索。
  // sessionId 优先取消息自带（assistant 消息携带 agentResult.sessionId），否则懒加载当前对话最近 session。
  const handleFork = async (messageId, sessionIdHint) => {
    if (loading || forkingMessageId) return
    if (!currentConversationId || !messageId) return
    setForkingMessageId(messageId)
    try {
      let sid = sessionIdHint
      if (!sid) {
        try {
          const res = await agentApi.listSessions(1, 100)
          const s = (res?.items || []).find((it) => it.conversation_id === currentConversationId)
          sid = s?.id
        } catch (e) { /* ignore，下方兜底报错 */ }
      }
      if (!sid) {
        setError('无法分叉：未找到当前会话')
        return
      }
      const res = await agentApi.forkSession(sid, messageId)
      const newConvId = res?.conversation_id
      if (!newConvId) {
        setError('分叉失败：未返回新会话')
        return
      }
      // 本地建立分叉对话占位，随后从服务端拉取复制的历史消息
      const parent = currentConversation
      setConversations((prev) => [
        {
          id: newConvId,
          title: (parent?.title || '分叉对话') + ' (分叉)',
          model: parent?.model || '',
          provider: parent?.provider || '',
          teamId: parent?.teamId || '',
          messages: [],
          status: 'active'
        },
        ...prev
      ])
      await refreshMessages(newConvId)
      // 记录分支链接：新对话 -> 父；父 -> 新对话（去重）
      setForkLinks((prev) => {
        const next = { ...prev }
        next[newConvId] = { parent: currentConversationId, children: next[newConvId]?.children || [] }
        const p = next[currentConversationId] || { parent: null, children: [] }
        next[currentConversationId] = {
          ...p,
          children: Array.from(new Set([...(p.children || []), newConvId]))
        }
        return next
      })
      switchConversation(newConvId)
      setAgentSessionRefresh((n) => n + 1)
    } catch (e) {
      console.error('分叉失败:', e)
      setError('分叉失败：' + (e?.message || '未知错误'))
    } finally {
      setForkingMessageId(null)
    }
  }

  const handleNewChat = () => {
    const conv = createConversation()
    // 快照当前模型配置到新对话（model 身份），切换对话时据此恢复
    conv.model = currentProfile?.modelName || ''
    conv.provider = currentProfile?.provider || ''
    const next = [conv, ...conversations]
    updateConversations(next)
    switchConversation(conv.id)
    setLastUsage(null) // AR-07：新对话清空用量状态条
  }

  const handleDeleteConversation = async (e, id) => {
    e.stopPropagation()
    try {
      await conversationApi.delete(id)
    } catch (err) {
      // 后端返回 404/对话不存在时，视为已删除，继续清理本地状态。
      // 新建后未同步到后端的对话即属于这种情况。
      const msg = err.message || ''
      const isNotFound = /不存在|not found|404/i.test(msg)
      if (!isNotFound) {
        console.error('删除对话失败:', err)
        setError('删除对话失败')
        return
      }
    }
    const next = deleteConversation(conversations, id)
    updateConversations(next)
    if (id === currentConversationId) {
      switchConversation(next[0]?.id)
    }
  }

  const handleDeleteAllConversations = async () => {
    // AR-05-O5：用 ConfirmDialog 替代原生 confirm()
    const ok = await new Promise((resolve) => {
      setConfirmState({
        open: true,
        title: '删除全部对话',
        message: '确定删除所有对话吗？此操作不可恢复。',
        confirmText: '删除',
        onConfirm: () => { setConfirmState({ open: false }); resolve(true) },
        onCancel: () => { setConfirmState({ open: false }); resolve(false) }
      })
    })
    if (!ok) return
    // 串行删除，避免并发过多请求；未同步到后端的对话删除会失败，忽略即可
    for (const conv of conversations) {
      try {
        await conversationApi.delete(conv.id)
      } catch (err) {
        console.warn('删除对话跳过:', conv.id, err.message)
      }
    }
    const welcome = createConversation()
    updateConversations([welcome])
    switchConversation(welcome.id)
    // AR-05-O5：用 ConfirmDialog 替代原生 alert()
    setConfirmState({
      open: true,
      title: '已完成',
      message: '对话历史已清空',
      danger: false,
      confirmText: '好的',
      cancelText: '关闭',
      onConfirm: () => setConfirmState({ open: false }),
      onCancel: () => setConfirmState({ open: false })
    })
  }

  const updateCurrentMessages = (updater) => {
    updateConversations((prev) => {
      const conv = prev.find((c) => c.id === currentConversationId)
      if (!conv) return prev
      const nextMessages = typeof updater === 'function' ? updater(conv.messages) : updater
      return prev.map((c) =>
        c.id === conv.id ? { ...c, messages: nextMessages, updatedAt: Date.now() } : c
      )
    })
  }

  // 按消息 ID 更新指定对话中的单条消息
  const updateMessage = useCallback((conversationId, messageId, updater) => {
    updateConversations((prev) => {
      const conv = prev.find((c) => c.id === conversationId)
      if (!conv) return prev
      return prev.map((c) => {
        if (c.id !== conv.id) return c
        return {
          ...c,
          messages: c.messages.map((m) =>
            m.id === messageId ? (typeof updater === 'function' ? updater(m) : updater) : m
          ),
          updatedAt: Date.now()
        }
      })
    })
  }, [])

  // Agent 工作流步骤实时同步到当前 assistant 消息气泡（必须在 updateMessage 定义之后）
  // 所有派生字段都从 agentSteps 计算，避免与 useAgent 中独立维护的 toolSteps/resources 等状态不同步
  useEffect(() => {
    if (!currentConversation?.id || !streamingAgentMsgId) return
    if (agentSteps.length === 0 && agentStatus !== 'executing' && agentStatus !== 'answering') return
    updateMessage(currentConversation.id, streamingAgentMsgId, (m) => {
      const answerSteps = agentSteps.filter((s) => s.type === 'answer')
      const currentAnswer = answerSteps.map((s) => s.content).join('') || m.content || ''

      const thinkingSteps = agentSteps.filter((s) => s.type === 'thinking')
      const currentReasoning = thinkingSteps.map((s) => s.content).join('')

      const intermediateSteps = agentSteps.filter((s) => s.type === 'intermediate')
      const currentIntermediate = intermediateSteps.map((s) => s.content).join('')

      const derivedToolSteps = agentSteps
        .filter((s) => s.type === 'tool_call' || s.type === 'tool_result')
        .map((s) => ({
          step: s.step,
          tool: s.tool,
          arguments: s.arguments,
          status: s.status,
          output: s.output,
          error: s.error
        }))

      const derivedResources = agentSteps
        .filter((s) => s.type === 'resource')
        .map((s) => ({
          resource_id: s.resource_id,
          file_name: s.file_name,
          mime_type: s.mime_type,
          download_url: s.download_url
        }))

      const warningStep = [...agentSteps].reverse().find((s) => s.type === 'warning')
      const errorStep = [...agentSteps].reverse().find((s) => s.type === 'error')

      return {
        ...m,
        steps: agentSteps,
        content: currentAnswer,
        answer: currentAnswer,
        reasoningContent: currentReasoning || m.reasoningContent || '',
        toolSteps: derivedToolSteps.length > 0 ? derivedToolSteps : m.toolSteps || [],
        intermediateContent: currentIntermediate || m.intermediateContent || '',
        resources: derivedResources.length > 0 ? derivedResources : m.resources || [],
        warning: warningStep?.message || m.warning || '',
        error: errorStep?.message || m.error || ''
      }
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [agentSteps, streamingAgentMsgId, currentConversation?.id, updateMessage])

  const updateProfiles = (next) => {
    setProfiles(next)
  }

  // Profile 持久化，按当前用户隔离
  useEffect(() => {
    saveProfiles(profiles, user?.user_id)
  }, [profiles, user?.user_id])

  useEffect(() => {
    saveCurrentProfileId(currentProfileId, user?.user_id)
  }, [currentProfileId, user?.user_id])

  const updateProfile = (id, patch) => {
    const next = profiles.map((p) => (p.id === id ? { ...p, ...patch } : p))
    updateProfiles(next)
  }

  const switchProfile = (id) => {
    setCurrentProfileId(id)
    // 切换模型 = 更新当前对话绑定的模型配置并同步后端
    const p = profiles.find((pp) => pp.id === id)
    if (p && currentConversation) {
      updateConversations((prev) =>
        prev.map((c) =>
          c.id === currentConversation.id
            ? { ...c, model: p.modelName, provider: p.provider, updatedAt: Date.now() }
            : c
        )
      )
      conversationApi
        .update(currentConversation.id, { model: p.modelName, provider: p.provider })
        .catch(() => {})
    }
  }

  /**
   * 确保 Ele Agent 临时凭证有效（与 App 端 EleAgentClient.ensureCredentials 对齐）
   * 实际调用 /chat/completions 时仍使用 JWT，避免临时 apiKey 无法通过后端认证。
   */
  const ensureEleAgentCredentials = async (profile) => {
    const cached = loadCachedCredentials(profile.id, user?.user_id)
    if (cached?.baseUrl) {
      return { baseUrl: resolveBaseUrl(cached.baseUrl) }
    }

    const { subProvider, subModel } = parseEleAgentModelName(profile.modelName)
    if (!subProvider || !subModel) {
      throw new Error('Ele Agent 模型名格式错误，应为 subProvider/subModel')
    }

    const data = await eleAgentApi.credentials(subProvider, subModel)
    if (!data?.baseUrl) throw new Error('Ele Agent 凭证响应为空')
    saveCachedCredentials(profile.id, data, user?.user_id)
    return { baseUrl: resolveBaseUrl(data.baseUrl) }
  }

  const handleAttachmentChange = useCallback(async (action) => {
    if (action.type === 'add') {
      try {
        const { attachment } = await fileToContentPart(action.file)
        setAttachments((prev) => [...prev, attachment])
      } catch (err) {
        setError(err.message || '文件处理失败')
      }
    } else if (action.type === 'remove') {
      setAttachments((prev) => prev.filter((a) => a.id !== action.id))
    } else if (action.type === 'reject') {
      setError(action.reason || '当前模型不支持该类型文件')
    }
  }, [])

  const handleSend = useCallback(async () => {
    if (!input.trim() && attachments.length === 0) return
    if (!currentProfile) {
      setError('请先配置模型：点击右上角设置图标')
      return
    }
    // BYOK（非 Ele Agent）自带 API Key，不依赖平台登录态，可直接对话；
    // Ele Agent / agent 走云端 LLM API，需登录态。
    if (!isLoggedIn && currentProfile.provider === 'ELE_AGENT') {
      setLoginOpen(true)
      return
    }
    if (!currentConversation) {
      handleNewChat()
      return
    }

    const messageContent = buildMessageContent(input, attachments)
    const userMessage = { role: 'user', content: messageContent, attachments: [...attachments] }
    const assistantMessage = { role: 'assistant', content: '', reasoningContent: '', toolSteps: [], warning: '' }
    const isFirstUser = !currentConversation.messages.some((m) => m.role === 'user')

    // Agent 工作流分支（仅 Ele Agent 模型支持）
    if (enableTools && supportsAgent) {
      // 先只追加用户消息并持久化到后端，Assistant 回答在 Agent 工作流过程中实时写入。
      updateConversations((prev) => {
        const conv = prev.find((c) => c.id === currentConversation.id)
        if (!conv) return prev
        return prev.map((c) =>
          c.id === conv.id
            ? { ...c, messages: [...c.messages, userMessage], updatedAt: Date.now() }
            : c
        )
      })
      // 保存用户消息；后端若自动生成标题会一并返回，立即同步到本地
      const savedTitle = await persistMessage(currentConversation.id, userMessage)
      if (savedTitle) {
        updateConversations((prev) =>
          prev.map((c) =>
            c.id === currentConversation.id
              ? { ...c, title: savedTitle, updatedAt: Date.now() }
              : c
          )
        )
      }
      // 首条消息发出后后端对话才被懒创建；此时把模型快照同步到后端，保证 reload 后可恢复
      if (isFirstUser) {
        conversationApi
          .update(currentConversation.id, {
            model: currentProfile?.modelName || '',
            provider: currentProfile?.provider || ''
          })
          .catch(() => {})
      }
      setInput('')
      setAttachments([])
      setLoading(true)
      setError('')

      // 同步当前对话的联网设置到本地状态和后端
      updateConversations((prev) =>
        prev.map((c) =>
          c.id === currentConversation.id
            ? { ...c, enableTools: true, enableWebSearch, searchProvider, updatedAt: Date.now() }
            : c
        )
      )
      conversationApi.update(currentConversation.id, {
        enable_tools: true,
        enable_web_search: enableWebSearch,
        search_provider: searchProvider
      }).catch(() => {})

      // 提前插入一个空的 assistant 占位消息，后续通过 streamingAgentMsgId 实时更新 steps
      const assistantMessage = {
        id: `agent_${currentConversation.id}_${Date.now()}`,
        role: 'assistant',
        content: '',
        steps: [],
        reasoningContent: '',
        toolSteps: [],
        intermediateContent: '',
        resources: [],
        toolSummary: '',
        warning: '',
        clientMessageId: `agent_client_${Date.now()}`,
        createdAt: Date.now()
      }
      updateConversations((prev) => {
        const conv = prev.find((c) => c.id === currentConversation.id)
        if (!conv) return prev
        return prev.map((c) =>
          c.id === conv.id
            ? { ...c, messages: [...c.messages, assistantMessage], updatedAt: Date.now() }
            : c
        )
      })
      setStreamingAgentMsgId(assistantMessage.id)

      let agentResult = null
      setLastUsage(null) // AR-07：执行开始清空旧用量，避免流式期间显示陈旧数据
      try {
        agentResult = await executeAgent({
          conversationId: currentConversation.id,
          message: input,
          attachments,
          history: buildHistoryMessages([...currentConversation.messages, userMessage]),
          model: currentProfile.modelName,
          provider: 'eleagent',
          baseUrl: '',
          apiKey: '',
          enableTools: true,
          enableWebSearch,
          searchProvider,
          assistantId,
          cwd
        })
      } catch (err) {
        const errorMsg = err.message || 'Agent 执行失败'
        const errorStep = { type: 'error', message: errorMsg }
        // 同步更新本地引用，确保最终持久化时保留错误信息
        assistantMessage.content = errorMsg
        assistantMessage.steps = [...assistantMessage.steps, errorStep]
        updateMessage(currentConversation.id, assistantMessage.id, (m) => ({
          ...m,
          content: errorMsg,
          steps: [...m.steps, errorStep]
        }))
        setError(errorMsg)
      }
      setLastUsage(agentResult?.usage || null) // AR-07：用量可见性（claw 无 cost_amount，状态条自动裁剪成本）

      // Agent 流式结束或失败后，统一用 agentResult 中的最终字段覆盖占位消息，
      // 并做最终持久化。toolSummary 拼入 content 作为历史上下文。
      const agentNewTitle = isFirstUser ? generateTitle(contentToText(messageContent)) : null
      const finalSteps = agentResult?.steps || assistantMessage.steps || []
      const finalAnswer = agentResult?.answer || agentResult?.intermediateAnswer || assistantMessage.answer || ''
      const finalToolSummary = agentResult?.toolSummary || assistantMessage.toolSummary || ''
      const isCancelled = agentResult?.cancelled === true
      const hasErrorStep = finalSteps.some((s) => s.type === 'error')
      const hasWarningStep = finalSteps.some((s) => s.type === 'warning')
      const fallbackContent = isCancelled
        ? '已停止生成'
        : hasErrorStep
        ? 'Agent 执行出错'
        : hasWarningStep
        ? 'Agent 已完成，但未生成有效回答'
        : 'Agent 未生成回答'
      const finalContent = finalToolSummary && finalAnswer
        ? `${finalToolSummary}\n\n${finalAnswer}`
        : (finalAnswer || assistantMessage.content || fallbackContent)
      const finalMessage = {
        ...assistantMessage,
        content: finalContent,
        answer: finalAnswer,
        steps: finalSteps,
        reasoningContent: keepThinking ? (agentResult?.reasoningContent || '') : '',
        toolSteps: (agentResult?.toolSteps || []).map((s) => ({ ...s })),
        intermediateContent: agentResult?.intermediateAnswer || '',
        resources: (agentResult?.resources || []).map((r) => ({ ...r })),
        toolSummary: finalToolSummary,
        warning: agentResult?.warning || '',
        sessionId: agentResult?.sessionId || '',
        clientMessageId: agentResult?.sessionId
          ? `agent_assistant_${agentResult.sessionId}`
          : assistantMessage.clientMessageId
      }

      updateConversations((prev) => {
        const conv = prev.find((c) => c.id === currentConversation.id)
        if (!conv) return prev
        return prev.map((c) =>
          c.id === conv.id
            ? {
                ...c,
                messages: c.messages.map((m) => (m.id === assistantMessage.id ? finalMessage : m)),
                title: agentNewTitle || c.title,
                updatedAt: Date.now()
              }
            : c
        )
      })

      // 后端已自动保存 assistant 消息；前端用相同 client_message_id 覆盖更新，
      // 以便在开启“保留思考过程”时把 reasoning_content 一并写入。
      const assistantMessageToPersist = keepThinking
        ? finalMessage
        : { ...finalMessage, reasoningContent: '' }
      persistMessage(currentConversation.id, assistantMessageToPersist)

      // 第一条用户消息后，把生成的标题同步到后端，避免刷新后显示"新对话"
      if (agentNewTitle) {
        conversationApi.update(currentConversation.id, { title: agentNewTitle }).catch(() => {})
      }
      setStreamingAgentMsgId(null)
      resetAgent()
      setLoading(false)
      setAgentSessionRefresh((n) => n + 1)
      return
    }

    const normalNewTitle = isFirstUser ? generateTitle(contentToText(messageContent)) : null
    // 给助手消息一个 clientMessageId，持久化到后端时可用作去重键
    assistantMessage.clientMessageId = `assistant_${currentConversation.id}_${Date.now()}_${Math.random().toString(36).slice(2, 7)}`
    updateConversations((prev) => {
      const conv = prev.find((c) => c.id === currentConversation.id)
      if (!conv) return prev
      return prev.map((c) =>
        c.id === conv.id
          ? {
              ...c,
              messages: [...c.messages, userMessage, assistantMessage],
              title: normalNewTitle || c.title,
              updatedAt: Date.now()
            }
          : c
      )
    })
    // 保存用户消息；后端若自动生成标题会一并返回，立即同步到本地
    const savedTitle = await persistMessage(currentConversation.id, userMessage)
    const finalTitle = savedTitle || normalNewTitle
    if (finalTitle) {
      updateConversations((prev) =>
        prev.map((c) =>
          c.id === currentConversation.id
            ? { ...c, title: finalTitle, updatedAt: Date.now() }
            : c
        )
      )
    }
    // 兜底：后端未生成标题时，再显式 PATCH 同步前端生成的标题
    if (!savedTitle && normalNewTitle) {
      conversationApi.update(currentConversation.id, { title: normalNewTitle }).catch(() => {})
    }
    // 首条消息发出后后端对话才被懒创建；此时把模型快照同步到后端，保证 reload 后可恢复
    if (isFirstUser) {
      conversationApi
        .update(currentConversation.id, {
          model: currentProfile?.modelName || '',
          provider: currentProfile?.provider || ''
        })
        .catch(() => {})
    }

    setInput('')
    setAttachments([])
    setLoading(true)
    setError('')
    abortRef.current = false

    try {
      // 发送给 API 时只保留 role 与 content，剔除前端附件元数据，并过滤掉空内容的助手消息
      const chatMessages = buildHistoryMessages([...currentConversation.messages, userMessage])
      let chatUrl, chatToken, requestBody

      if (currentProfile.provider === 'ELE_AGENT') {
        const creds = await ensureEleAgentCredentials(currentProfile)
        chatUrl = `${creds.baseUrl}/chat/completions`
        // 发送时再次从 storage 读取 token，避免切换账户后闭包仍持有旧 JWT
        chatToken = getItem('token') || token
        requestBody = {
          provider: 'eleagent',
          model: currentProfile.modelName,
          messages: chatMessages,
          stream: true
        }
        if (currentProfile.systemPrompt) {
          requestBody.messages = [{ role: 'system', content: currentProfile.systemPrompt }, ...requestBody.messages]
        }
      } else {
        if (!currentProfile.baseUrl || !currentProfile.apiKey || !currentProfile.modelName) {
          throw new Error('请完善自定义模型配置（Base URL / API Key / 模型名）')
        }
        chatUrl = `${currentProfile.baseUrl.replace(/\/$/, '')}/chat/completions`
        chatToken = currentProfile.apiKey
        requestBody = {
          model: currentProfile.modelName,
          messages: chatMessages,
          stream: true
        }
        if (currentProfile.systemPrompt) {
          requestBody.messages = [{ role: 'system', content: currentProfile.systemPrompt }, ...requestBody.messages]
        }
      }

      for await (const chunk of streamChat(chatUrl, requestBody, chatToken)) {
        if (abortRef.current) break
        if (chunk.type === 'content') {
          // 去掉模型开头的空换行，避免回复顶部出现空白行
          assistantMessage.content = (assistantMessage.content + chunk.content).replace(/^\n+/, '')
          updateCurrentMessages((prev) => {
            const next = [...prev]
            next[next.length - 1] = { ...assistantMessage }
            return next
          })
        } else if (chunk.type === 'reasoning') {
          // Kimi / DeepSeek 等模型的思考过程
          assistantMessage.reasoningContent += chunk.content
          updateCurrentMessages((prev) => {
            const next = [...prev]
            next[next.length - 1] = { ...assistantMessage }
            return next
          })
        }
      }
    } catch (err) {
      const msg = err.message || '对话失败，请稍后重试'
      // 上游模型 Key 无效时给更友好的提示
      const displayError = /401|Api key is invalid|鉴权|Unauthorized/i.test(msg)
        ? '上游模型鉴权失败：请检查 API Key 是否正确，或联系管理员更新 Ele Agent 模型 Key。'
        : msg
      setError(displayError)
      // 将错误提示绑定到当前对话，切换走后再回来仍可见
      updateConversations((prev) =>
        prev.map((c) => (c.id === currentConversationId ? { ...c, error: displayError } : c))
      )
      assistantMessage.content = '（回复失败）'
      updateCurrentMessages((prev) => {
        const next = [...prev]
        next[next.length - 1] = { ...assistantMessage }
        return next
      })
    } finally {
      // 流正常结束但模型未返回任何内容时，给用户明确提示，避免空气泡
      if (
        !abortRef.current &&
        assistantMessage.content === '' &&
        assistantMessage.reasoningContent === ''
      ) {
        const isVision = Array.isArray(messageContent) && messageContent.some((p) => p.type === 'image_url')
        const hasFile = attachments.some((a) => a.type === 'file')
        const modelLabel = currentProfile
          ? `（当前模型：${currentProfile.name || currentProfile.modelName}）`
          : ''
        if (isVision) {
          assistantMessage.content = `模型未返回任何内容${modelLabel}。请确认当前模型支持图片理解，或切换到视觉模型（如 moonshot-v1-vision-preview / kimi-k2.6）后重试。`
        } else if (hasFile) {
          assistantMessage.content = `模型未返回任何内容${modelLabel}。请确认文件为文本/代码/图片格式且当前模型支持解析；二进制文档（如 PDF/Word）可能无法直接分析。`
        } else {
          assistantMessage.content = `模型未返回任何内容${modelLabel}，请稍后重试。`
        }
        updateCurrentMessages((prev) => {
          const next = [...prev]
          next[next.length - 1] = { ...assistantMessage }
          return next
        })
      }

      // 将最终助手消息持久化到后端，失败时不阻塞 UI
      // 若用户未开启“保留思考过程”，持久化时不传 reasoning_content，减少后端存储
      const assistantMessageToPersist = keepThinking
        ? assistantMessage
        : { ...assistantMessage, reasoningContent: '' }
      persistMessage(currentConversation.id, assistantMessageToPersist)

      setLoading(false)
    }
  }, [input, attachments, isLoggedIn, currentProfile, currentConversation, conversations, token, user?.user_id, models, enableTools, executeAgent, persistMessage])

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      handleSend()
    }
  }

  const handlePaste = useCallback(
    (e) => {
      const items = Array.from(e.clipboardData?.items || [])
      let hasFile = false
      for (const item of items) {
        if (item.kind === 'file') {
          const file = item.getAsFile()
          if (file) {
            hasFile = true
            handleAttachmentChange({ type: 'add', file })
          }
        }
      }
      if (hasFile) {
        e.preventDefault()
      }
    },
    [handleAttachmentChange]
  )

  // Ele Agent 模型是否支持视觉；自定义模型（BYOK）未知，默认允许图片上传
  const currentEleAgentModel = useMemo(() => {
    if (!currentProfile || currentProfile.provider !== 'ELE_AGENT') return null
    const fullName = currentProfile.modelName
    return models.find((m) => `${m.provider}/${m.model_name}` === fullName)
  }, [currentProfile, models])

  const supportsVision = (() => {
    if (!currentProfile || currentProfile.provider !== 'ELE_AGENT') return true
    return currentEleAgentModel?.supports_vision === true
  })()
  const supportsImage = currentEleAgentModel?.supports_image === true
  const supportsVideo = currentEleAgentModel?.supports_video === true
  const supportsVisualGeneration = supportsImage || supportsVideo

  const currentLabel = currentProfile
    ? `${currentProfile.name} · ${PROVIDERS[currentProfile.provider]?.label}`
    : '未配置模型'

  const fileAccept = supportsVision
    ? 'image/*,.txt,.md,.markdown,.json,.csv,.html,.css,.js,.jsx,.ts,.tsx,.py,.go,.java,.c,.cpp,.h,.xml,.yaml,.yml'
    : '.txt,.md,.markdown,.json,.csv,.html,.css,.js,.jsx,.ts,.tsx,.py,.go,.java,.c,.cpp,.h,.xml,.yaml,.yml'

  const handleFileSelect = useCallback(
    (e) => {
      const files = Array.from(e.target.files || [])
      if (files.length === 0) return
      files.forEach((file) => {
        if (!supportsVision && isImageFile(file)) {
          handleAttachmentChange({ type: 'reject', reason: '当前模型不支持图片理解，请切换到视觉模型（VLM）后重试。' })
          return
        }
        handleAttachmentChange({ type: 'add', file })
      })
      e.target.value = ''
    },
    [handleAttachmentChange, supportsVision]
  )

  if (!isLoggedIn) {
    return (
      <div className="pt-24 px-4 text-center">
        <div className="max-w-md mx-auto card">
          <Bot className="w-12 h-12 text-eleball-primary mx-auto mb-4" />
          <h2 className="text-xl font-bold text-eleball-text mb-2">登录后体验完整对话</h2>
          <p className="text-sm text-eleball-text-secondary mb-6">
            与云端共享同一账户；登录后对话历史保存在本机，可在各浏览器间同步。
          </p>
          <button onClick={() => setLoginOpen(true)} className="btn-primary w-full justify-center">
            登录 / 注册
          </button>
        </div>
        <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />
      </div>
    )
  }

  return (
    <div className="relative h-[calc(100dvh-64px)] flex overflow-hidden bg-eleball-bg">
      {/* 侧边栏：对话历史 */}
      {sidebarOpen && (
        <div
          className="fixed inset-0 bg-black/30 z-40 md:hidden"
          onClick={() => setSidebarOpen(false)}
        />
      )}
      <aside
        className={`fixed md:static left-0 top-16 md:top-0 bottom-0 w-64 bg-eleball-surface border-r border-eleball-outline-variant z-50 flex flex-col overflow-y-auto transition-transform duration-200 md:h-[calc(100dvh-64px)] ${
          sidebarOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0 md:block'
        }`}
      >
        <div className="p-3 border-b border-eleball-outline-variant flex items-center justify-between flex-shrink-0">
          <span className="font-medium text-sm text-eleball-text">对话历史</span>
          <div className="flex items-center gap-1">
            <button
              onClick={handleNewChat}
              className="p-1.5 rounded-lg text-eleball-primary hover:bg-eleball-primary-light transition-colors"
              aria-label="新对话" title="新对话"
            >
              <Plus className="w-4 h-4" />
            </button>
            <button
              onClick={() => setTeamModalOpen(true)}
              className="p-1.5 rounded-lg text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors"
              aria-label="分组管理" title="分组管理"
            >
              <Folders className="w-4 h-4" />
            </button>
            <button
              onClick={handleDeleteAllConversations}
              className="p-1.5 rounded-lg text-red-500 hover:bg-red-50 transition-colors"
              aria-label="删除全部对话" title="删除全部对话"
            >
              <Trash2 className="w-4 h-4" />
            </button>
            <button
              onClick={() => setSidebarOpen(false)}
              className="md:hidden p-1.5 rounded-lg text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors"
            >
              <X className="w-4 h-4" />
            </button>
          </div>
        </div>
        <div className="p-2 space-y-2">
          {/* 各分组：仅渲染有对话落入的组，组名可点击进入分组详情 */}
          {teams.map((team) => {
            const convs = groupedConversations.get(team.id) || []
            if (convs.length === 0) return null
            return (
              <div key={team.id}>
                <button
                  onClick={() => navigate(`/teams/${team.id}`)}
                  className="flex items-center gap-1 px-2 py-1 w-full text-left hover:bg-eleball-surface-variant rounded-lg transition-colors"
                  aria-label="查看分组详情" title="查看分组详情"
                >
                  <ChevronRight className="w-3 h-3 text-eleball-text-tertiary flex-shrink-0" />
                  <span className="text-xs font-medium text-eleball-text-secondary truncate">{team.name}</span>
                  <span className="text-[10px] text-eleball-text-tertiary flex-shrink-0">({convs.length})</span>
                </button>
                <div className="space-y-0.5">
                  {convs.map((conv) => (
                    <ConversationItem
                      key={conv.id}
                      conv={conv}
                      isActive={conv.id === currentConversationId}
                      onSelect={switchConversation}
                      onDelete={handleDeleteConversation}
                      onMove={setMoveMenuConvId}
                    />
                  ))}
                </div>
              </div>
            )
          })}
          {/* 未分组 */}
          {(() => {
            const convs = groupedConversations.get('') || []
            if (convs.length === 0) return null
            return (
              <div>
                {teams.length > 0 && (
                  <div className="flex items-center gap-1 px-2 py-1">
                    <span className="text-xs font-medium text-eleball-text-tertiary">未分组</span>
                    <span className="text-[10px] text-eleball-text-tertiary">({convs.length})</span>
                  </div>
                )}
                <div className="space-y-0.5">
                  {convs.map((conv) => (
                    <ConversationItem
                      key={conv.id}
                      conv={conv}
                      isActive={conv.id === currentConversationId}
                      onSelect={switchConversation}
                      onDelete={handleDeleteConversation}
                      onMove={setMoveMenuConvId}
                    />
                  ))}
                </div>
              </div>
            )
          })()}
        </div>
        <div className="p-3 border-t border-eleball-outline-variant flex-shrink-0">
          <AgentSessionList onRefresh={agentSessionRefresh} onSelect={handleSelectSession} />
        </div>
      </aside>

      {/* 主对话区 */}
      <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
        {/* Chat Header - 粘顶 */}
        <div className="flex-shrink-0 z-30 bg-eleball-surface border-b border-eleball-outline-variant px-4 py-3 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <button
              onClick={() => setSidebarOpen(true)}
              className="md:hidden p-1.5 rounded-full text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors"
              aria-label="对话列表" title="对话列表"
            >
              <Menu className="w-4 h-4" />
            </button>
            <Bot className="w-5 h-5 text-eleball-primary" />
            <span className="font-medium text-eleball-text">Eleball</span>
          </div>
          {/* 右侧操作区：限制最大宽度并在空间不足时收缩，避免横屏 H5 上设置按钮被挤出 */}
          <div className="flex items-center gap-2 sm:gap-3 min-w-0">
            <button
              onClick={handleNewChat}
              className="hidden sm:inline-flex items-center gap-1 text-xs font-medium text-eleball-primary bg-eleball-primary-light/50 hover:bg-eleball-primary-light px-3 py-1.5 rounded-full transition-colors"
              aria-label="新对话" title="新对话"
            >
              <Plus className="w-3.5 h-3.5" />
              新对话
            </button>
            {balance && (
              <Link to="/recharge" className="hidden sm:inline-block text-xs text-eleball-text-secondary hover:text-eleball-primary truncate max-w-[80px]">
                余额：{balance.danwan} 弹丸
              </Link>
            )}

            {/* 模型快速切换 */}
            <div className="relative min-w-0">
              <select
                value={currentProfileId}
                onChange={(e) => switchProfile(e.target.value)}
                className="appearance-none bg-eleball-primary-light/50 hover:bg-eleball-primary-light text-xs font-medium text-eleball-primary pl-3 pr-8 py-1.5 rounded-full border-0 outline-none cursor-pointer transition-colors max-w-[120px] sm:max-w-[180px] truncate"
              >
                {profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name} · {PROVIDERS[p.provider]?.label}
                  </option>
                ))}
              </select>
              <ChevronDown className="w-3.5 h-3.5 text-eleball-primary absolute right-2.5 top-1/2 -translate-y-1/2 pointer-events-none" />
            </div>

            {/* AR-11：工作目录选择 + 文件浏览器侧栏开关（仅 claw 本地） */}
            <button
              onClick={() => setCwdPickerOpen(true)}
              className="hidden sm:inline-flex items-center gap-1 text-xs font-medium text-eleball-text-secondary bg-eleball-surface hover:bg-eleball-surface-variant px-2.5 py-1.5 rounded-full border border-eleball-outline transition-colors max-w-[140px]"
              aria-label="选择工作目录" title={cwd || '选择工作目录'}
            >
              <Folder className="w-3.5 h-3.5 flex-shrink-0" />
              <span className="truncate">{cwd ? cwd.split(/[\\/]/).pop() : '工作目录'}</span>
            </button>
            <button
              onClick={() => setFilePanelOpen((v) => !v)}
              className={`hidden sm:inline-flex flex-shrink-0 p-1.5 rounded-full transition-colors ${filePanelOpen ? 'text-eleball-primary bg-eleball-primary-light' : 'text-eleball-text-secondary bg-eleball-surface hover:bg-eleball-surface-variant'}`}
              aria-label="文件浏览器" title="文件浏览器"
            >
              <PanelRight className="w-4 h-4" />
            </button>

            {/* 模型配置入口：固定尺寸，防止被 flex 压缩导致显示不全 */}
            <button
              onClick={() => setSettingsOpen(true)}
              className="flex-shrink-0 p-1.5 rounded-full text-eleball-primary bg-eleball-primary-light/50 hover:bg-eleball-primary-light transition-colors"
              aria-label="模型配置" title="模型配置"
            >
              <Settings className="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        {/* AR-07：用量可见性状态条（tokens/步数/成本/上下文规模），Agent 完成后展示；claw 无 cost_amount 自动裁剪成本 */}
        {lastUsage && agentStatus === 'done' && !loading && (
          <div className="flex-shrink-0 bg-eleball-surface/95 border-b border-eleball-outline-variant px-4 py-1 flex items-center gap-3 text-[11px] text-eleball-text-secondary overflow-x-auto">
            <span className="inline-flex items-center gap-1 whitespace-nowrap">
              <Check className="w-3 h-3 text-green-500" /> 完成
            </span>
            {lastUsage.total_tokens != null && (
              <span className="whitespace-nowrap">{formatTokens(lastUsage.total_tokens)} tokens</span>
            )}
            {lastUsage.step_count > 0 && (
              <span className="whitespace-nowrap">{lastUsage.step_count} 步</span>
            )}
            {lastUsage.prompt_tokens != null && (
              <span className="whitespace-nowrap">上下文 {formatTokens(lastUsage.prompt_tokens)}</span>
            )}
            {lastUsage.cost_amount != null && (
              <span className="whitespace-nowrap">{lastUsage.cost_amount} 弹丸</span>
            )}
          </div>
        )}

        {/* AR-12：会话分叉分支导航（父对话/子分叉跳转）；无分叉关系时组件返回 null */}
        <div className="flex-shrink-0 bg-eleball-surface border-b border-eleball-outline-variant px-4 py-1 flex items-center gap-2">
          <BranchNavigator
            currentConversationId={currentConversationId}
            forkLinks={forkLinks}
            conversations={conversations}
            onNavigate={switchConversation}
          />
        </div>

        {/* Messages */}
        <div className="flex-1 overflow-y-auto min-h-0 px-4 py-4 space-y-4">
          {(currentConversation?.messages || []).map((message, idx) => (
            <MessageBubble
              key={idx}
              message={message}
              isLast={idx === currentConversation.messages.length - 1}
              loading={loading}
              onFork={handleFork}
              forkingMessageId={forkingMessageId}
            />
          ))}

          {error && (
            <div className="flex items-center gap-2 text-sm text-eleball-error bg-red-50 rounded-xl px-3 py-2">
              <AlertCircle className="w-4 h-4" />
              {error}
            </div>
          )}
          <div ref={messagesEndRef} />
        </div>

        {/* Input */}
        <div className="flex-shrink-0 bg-eleball-surface border-t border-eleball-outline-variant px-3 pt-3 pb-[calc(0.75rem+env(safe-area-inset-bottom))]">
          <div className="max-w-3xl mx-auto">
            <div className="border border-eleball-outline rounded-2xl bg-white shadow-sm overflow-hidden focus-within:border-eleball-primary/40 focus-within:shadow-md transition-colors">
              <textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                onPaste={handlePaste}
                placeholder="输入消息，或粘贴/上传图片、文件…"
                rows={1}
                className="w-full resize-none border-0 px-4 pt-3 pb-2 focus:outline-none focus:ring-0 max-h-32 bg-transparent text-eleball-text placeholder:text-eleball-text-tertiary"
                style={{ minHeight: '44px' }}
              />
              {attachments.length > 0 && (
                <div className="flex flex-wrap gap-2 px-4 pb-2">
                  {attachments.map((att) => (
                    <div
                      key={att.id}
                      className="flex items-center gap-1.5 px-2 py-1 rounded-lg bg-eleball-primary-light/50 text-eleball-text text-xs border border-eleball-outline-variant"
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
                        onClick={() => handleAttachmentChange({ type: 'remove', id: att.id })}
                        disabled={loading}
                        className="p-0.5 rounded hover:bg-eleball-primary/20 text-eleball-text-secondary disabled:opacity-50"
                        aria-label="移除" title="移除"
                      >
                        <X className="w-3 h-3" />
                      </button>
                    </div>
                  ))}
                </div>
              )}
              <div className="flex items-center justify-between px-2 pb-2">
                <div className="flex items-center gap-2 flex-wrap">
                  <input
                    ref={fileInputRef}
                    type="file"
                    multiple
                    accept={fileAccept}
                    onChange={handleFileSelect}
                    disabled={loading}
                    className="hidden"
                  />
                  <button
                    type="button"
                    onClick={() => fileInputRef.current?.click()}
                    disabled={loading}
                    title={supportsVision ? '上传图片或文件' : '上传文件（当前模型不支持图片）'}
                    className="p-2 rounded-full text-eleball-text-secondary hover:bg-eleball-primary-light hover:text-eleball-primary transition-colors disabled:opacity-50"
                  >
                    <Plus className="w-5 h-5" />
                  </button>
                  <AgentSwitch
                    checked={enableTools}
                    onChange={setEnableTools}
                    disabled={!supportsAgent || loading}
                  />
                  {/* AR-13 O12：< sm 折叠次级工具开关到「更多」popover */}
                  <button
                    type="button"
                    onClick={() => setMoreToolsOpen((v) => !v)}
                    aria-label={moreToolsOpen ? '收起工具' : '更多工具'} title={moreToolsOpen ? '收起工具' : '更多工具'}
                    className="sm:hidden inline-flex items-center gap-1 px-2 py-1.5 rounded-full text-xs font-medium border bg-transparent text-eleball-text-secondary border-eleball-outline hover:bg-gray-50 hover:text-eleball-text transition-colors"
                  >
                    <MoreHorizontal className="w-3.5 h-3.5" />
                    <span>{moreToolsOpen ? '收起' : '更多'}</span>
                  </button>
                  <div className={`${moreToolsOpen ? 'flex' : 'hidden'} sm:flex items-center gap-2 flex-wrap`}>
                  <button
                    type="button"
                    onClick={() => setKeepThinking((v) => !v)}
                    aria-label="开启后将把模型的思考过程一并保存到对话历史" title="开启后将把模型的思考过程一并保存到对话历史"
                    className={[
                      'inline-flex items-center gap-1 px-2 py-1.5 rounded-full text-xs font-medium border transition-colors',
                      keepThinking
                        ? 'bg-purple-50 text-purple-600 border-purple-200 hover:bg-purple-100'
                        : 'bg-transparent text-eleball-text-secondary border-eleball-outline hover:bg-gray-50 hover:text-eleball-text'
                    ].join(' ')}
                  >
                    <span>💭</span>
                    <span>思考</span>
                  </button>
                  {enableTools && supportsAgent && (
                    <button
                      type="button"
                      onClick={() => setEnableWebSearch((v) => !v)}
                      aria-label="启用后 Agent 可调用 SearchWeb / FetchURL 联网工具" title="启用后 Agent 可调用 SearchWeb / FetchURL 联网工具"
                      className={[
                        'inline-flex items-center gap-1 px-2 py-1.5 rounded-full text-xs font-medium border transition-colors',
                        enableWebSearch
                          ? 'bg-blue-50 text-blue-600 border-blue-200 hover:bg-blue-100'
                          : 'bg-transparent text-eleball-text-secondary border-eleball-outline hover:bg-gray-50 hover:text-eleball-text'
                      ].join(' ')}
                    >
                      <Globe className="w-3.5 h-3.5" />
                      <span>联网</span>
                    </button>
                  )}
                  {enableTools && enableWebSearch && availableSearchProviders.length > 0 && (
                    <div className="relative">
                      <select
                        value={searchProvider}
                        onChange={(e) => setSearchProvider(e.target.value)}
                        disabled={loading}
                        aria-label="选择联网搜索源" title="选择联网搜索源"
                        className="appearance-none bg-blue-50 hover:bg-blue-100 text-blue-600 text-xs font-medium pl-2.5 pr-7 py-1.5 rounded-full border border-blue-200 outline-none cursor-pointer transition-colors max-w-[120px] truncate disabled:opacity-50"
                      >
                        {availableSearchProviders.map((p) => (
                          <option key={p.name} value={p.name}>{p.label}</option>
                        ))}
                      </select>
                      <ChevronDown className="w-3 h-3 text-blue-600 absolute right-2 top-1/2 -translate-y-1/2 pointer-events-none" />
                    </div>
                  )}
                  {enableTools && enableWebSearch && availableSearchProviders.length === 0 && (
                    <span className="text-xs text-eleball-text-secondary">未配置搜索源</span>
                  )}
                  {enableTools && supportsAgent && (
                    <button
                      type="button"
                      onClick={() => setAssistantPickerOpen(true)}
                      disabled={loading}
                      aria-label="选择本会话使用的助手（已激活秘技的组合）" title="选择本会话使用的助手（已激活秘技的组合）"
                      className="inline-flex items-center gap-1 px-2 py-1.5 rounded-full text-xs font-medium border transition-colors bg-purple-50 text-purple-600 border-purple-200 hover:bg-purple-100 disabled:opacity-50"
                    >
                      <span className="max-w-[96px] truncate">
                        {assistantId
                          ? assistants.find((a) => a.id === assistantId)?.name || '助手'
                          : '默认（全部已激活）'}
                      </span>
                      <ChevronDown className="w-3 h-3" />
                    </button>
                  )}
                  </div>
                </div>
                <div className="flex items-center gap-2 flex-shrink-0">
                  {supportsVisualGeneration && (
                    <button
                      type="button"
                      onClick={() => {
                        const tab = supportsVideo ? 'video' : 'image'
                        const prompt = input.trim()
                        navigate(`/visual?tab=${tab}${prompt ? `&prompt=${encodeURIComponent(prompt)}` : ''}`)
                      }}
                      className="flex items-center gap-1 px-2 py-1.5 rounded-lg text-sm text-eleball-primary hover:bg-eleball-primary-light/30 transition-colors"
                      aria-label="去视觉页创作" title="去视觉页创作"
                    >
                      {supportsVideo ? <Film className="w-4 h-4" /> : <Image className="w-4 h-4" />}
                      视觉创作
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => setSettingsOpen(true)}
                    className="flex items-center gap-1 px-2 py-1.5 rounded-lg text-sm text-eleball-text-secondary hover:bg-eleball-surface-variant hover:text-eleball-text transition-colors"
                    aria-label="切换模型" title="切换模型"
                  >
                    <span className="max-w-[120px] truncate">
                      {currentProfile?.name || currentProfile?.modelName || '未配置模型'}
                    </span>
                    <ChevronDown className="w-4 h-4" />
                  </button>
                  {/* AR-02：Agent 模式执行中显示停止按钮，调 abort 真正断连停止服务端工具循环 */}
                  {loading && enableTools && supportsAgent && (agentStatus === 'executing' || agentStatus === 'answering') ? (
                    <button
                      onClick={abortAgent}
                      className="p-3 rounded-full bg-red-500 hover:bg-red-600 text-white"
                      aria-label="停止生成" title="停止生成"
                    >
                      <Square className="w-5 h-5" />
                    </button>
                  ) : (
                    <button
                      onClick={handleSend}
                      disabled={loading || (!input.trim() && attachments.length === 0)}
                      className="btn-primary p-3 rounded-full disabled:opacity-50"
                      aria-label="发送消息"
                    >
                      <Send className="w-5 h-5" />
                    </button>
                  )}
                </div>
              </div>
            </div>
          </div>
          {/* AR-05：执行状态 aria-live 播报区（视觉隐藏，屏幕阅读器播报思考中/调用工具/生成回答/出错） */}
          <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">
            {statusAnnouncement}
          </div>
          <p className="text-center text-xs text-eleball-text-tertiary mt-2">
            {user?.nickname || user?.username} · {currentLabel}
          </p>
        </div>
      </div>

      {/* AR-11：文件浏览器/预览侧栏（仅 claw 本地，可折叠右栏；选中文件时切到 FileViewer） */}
      {filePanelOpen && (
        <aside className="hidden sm:flex flex-col w-80 lg:w-96 flex-shrink-0 border-l border-eleball-outline-variant bg-eleball-surface min-h-0">
          {selectedFile ? (
            <FileViewer cwd={cwd} path={selectedFile} onClose={() => setSelectedFile(null)} />
          ) : (
            <FileExplorer
              cwd={cwd}
              onOpenFile={(p) => setSelectedFile(p)}
              gitStatus={gitStatus}
              refreshKey={fileRefreshKey}
            />
          )}
        </aside>
      )}

      {/* AR-11：工作目录选择弹窗（选定后自动打开文件侧栏） */}
      <DirectoryPicker
        open={cwdPickerOpen}
        onClose={() => setCwdPickerOpen(false)}
        onSelect={(resolvedCwd) => {
          setCwd(resolvedCwd)
          setSelectedFile(null)
          setFilePanelOpen(true)
        }}
      />

      {/* AR-05-O5：确认弹窗（替代原生 confirm/alert） */}
      <ConfirmDialog
        open={confirmState.open}
        title={confirmState.title}
        message={confirmState.message}
        danger={confirmState.danger !== false}
        confirmText={confirmState.confirmText}
        cancelText={confirmState.cancelText}
        onConfirm={confirmState.onConfirm}
        onCancel={confirmState.onCancel}
      />

      <ModelSettings
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        profiles={profiles}
        currentProfileId={currentProfileId}
        eleagentModels={models}
        onProfilesChange={updateProfiles}
        onCurrentChange={switchProfile}
      />

      <AssistantPicker
        open={assistantPickerOpen}
        onClose={() => setAssistantPickerOpen(false)}
        assistants={assistants}
        currentId={assistantId}
        onPick={setAssistantId}
      />

      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />

      <TeamModal
        open={teamModalOpen}
        onClose={() => setTeamModalOpen(false)}
        teams={teams}
        onTeamsChange={loadTeams}
      />

      {/* 移动对话到分组：列出可选组 + 移出分组 */}
      {moveMenuConvId && (
        <div
          className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4"
          onClick={() => setMoveMenuConvId(null)}
        >
          <div
            className="bg-white rounded-2xl w-full max-w-xs max-h-[70vh] overflow-auto"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="p-3 border-b border-eleball-outline flex items-center justify-between">
              <h3 className="font-bold text-sm text-eleball-text">移动到分组</h3>
              <button
                onClick={() => setMoveMenuConvId(null)}
                className="text-eleball-text-tertiary hover:text-eleball-text"
              >
                &times;
              </button>
            </div>
            <div className="p-2 space-y-0.5">
              {teams.length === 0 && (
                <p className="text-xs text-eleball-text-secondary text-center py-4">
                  还没有分组，请先到「分组管理」创建
                </p>
              )}
              {teams.map((team) => (
                <button
                  key={team.id}
                  onClick={() => handleMoveConversation(moveMenuConvId, team.id)}
                  className="block w-full text-left px-3 py-2 rounded-lg text-sm text-eleball-text hover:bg-eleball-primary-light hover:text-eleball-primary transition-colors"
                >
                  {team.name}
                </button>
              ))}
              {teams.length > 0 && <div className="my-1 border-t border-eleball-outline-variant" />}
              <button
                onClick={() => handleMoveConversation(moveMenuConvId, '')}
                className="block w-full text-left px-3 py-2 rounded-lg text-sm text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors"
              >
                移出分组
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

// 对话侧栏条目：标题 + 时间 + 移动到组 / 删除（hover 显现）
function ConversationItem({ conv, isActive, onSelect, onDelete, onMove }) {
  return (
    <div
      onClick={() => onSelect(conv.id)}
      className={`group flex items-center gap-2 px-3 py-2 rounded-xl cursor-pointer transition-colors ${
        isActive
          ? 'bg-eleball-primary-light text-eleball-primary'
          : 'hover:bg-eleball-surface-variant text-eleball-text'
      }`}
    >
      <Bot className="w-4 h-4 flex-shrink-0" />
      <div className="flex-1 min-w-0">
        <p className="text-sm truncate">{conv.title || '新对话'}</p>
        <p className="text-[10px] opacity-70 truncate">
          {new Date(conv.updatedAt).toLocaleString('zh-CN', {
            month: 'short',
            day: 'numeric',
            hour: '2-digit',
            minute: '2-digit'
          })}
        </p>
      </div>
      <button
        onClick={(e) => {
          e.stopPropagation()
          onMove(conv.id)
        }}
        className={`p-1 rounded-md opacity-0 group-hover:opacity-100 transition-opacity ${
          isActive
            ? 'hover:bg-eleball-primary/20 text-eleball-primary'
            : 'hover:bg-eleball-outline text-eleball-text-secondary'
        }`}
        aria-label="移动到分组" title="移动到分组"
      >
        <FolderInput className="w-3.5 h-3.5" />
      </button>
      <button
        onClick={(e) => onDelete(e, conv.id)}
        className={`p-1 rounded-md opacity-0 group-hover:opacity-100 transition-opacity ${
          isActive
            ? 'hover:bg-eleball-primary/20 text-eleball-primary'
            : 'hover:bg-eleball-outline text-eleball-text-secondary'
        }`}
        aria-label="删除对话" title="删除对话"
      >
        <Trash2 className="w-3.5 h-3.5" />
      </button>
    </div>
  )
}

function isImageFile(file) {
  const imageTypes = ['image/png', 'image/jpeg', 'image/jpg', 'image/webp', 'image/gif']
  return imageTypes.includes(file.type) || /\.(png|jpe?g|webp|gif)$/i.test(file.name)
}

// ToolSummaryBlock 历史消息中的工具调用摘要，默认折叠，点击展开
function ToolSummaryBlock({ summary }) {
  const [expanded, setExpanded] = useState(false)
  if (!summary) return null

  return (
    <div className="rounded-xl border border-eleball-outline-variant bg-eleball-surface overflow-hidden">
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        className="w-full flex items-center justify-between px-3 py-2 text-xs text-eleball-text-secondary hover:bg-eleball-primary-light/30 transition-colors"
      >
        <span className="flex items-center gap-1.5">
          <Wrench className="w-3.5 h-3.5" />
          工具调用摘要
        </span>
        <ChevronDown className={`w-3.5 h-3.5 transition-transform ${expanded ? 'rotate-180' : ''}`} />
      </button>
      {expanded && (
        <div className="px-3 py-2 text-xs text-eleball-text-secondary whitespace-pre-wrap border-t border-eleball-outline-variant">
          {summary}
        </div>
      )}
    </div>
  )
}

function MessageBubble({ message, isLast, loading, onFork, forkingMessageId }) {
  const isUser = message.role === 'user'
  const isAssistant = message.role === 'assistant'

  // content 中已拼入工具摘要，展示时拆分为摘要与正文两部分
  const displayContent = useMemo(() => {
    if (!isAssistant || !message.toolSummary) return message.content
    const prefix = message.toolSummary + '\n\n'
    if (message.content && message.content.startsWith(prefix)) {
      return message.content.slice(prefix.length)
    }
    return message.content
  }, [isAssistant, message.content, message.toolSummary])

  return (
    <div className={`flex gap-3 ${isUser ? 'flex-row-reverse' : ''}`}>
      <div
        className={`w-7 h-7 rounded-full flex-shrink-0 flex items-center justify-center ${
          isUser ? 'bg-eleball-primary-light' : 'bg-eleball-surface-variant'
        }`}
      >
        {isUser ? (
          <UserIcon className="w-4 h-4 text-eleball-primary" />
        ) : (
          <Bot className="w-4 h-4 text-eleball-secondary" />
        )}
      </div>
      <div className="relative group max-w-[80%]">
        {isAssistant && message.toolSummary && (
          <div className="mb-2">
            <ToolSummaryBlock summary={message.toolSummary} />
          </div>
        )}
        {/* 新版 Agent 按时间线展示 thinking / tool / answer；旧消息无 steps 时回退到旧布局 */}
        {isAssistant && message.steps && message.steps.length > 0 ? (
          <div className="rounded-2xl px-4 py-2.5 text-sm leading-relaxed bg-eleball-surface-variant text-eleball-text rounded-bl-md">
            <AgentSteps steps={message.steps} loading={loading && isLast} />
          </div>
        ) : (
          <>
            {isAssistant && message.toolSteps && message.toolSteps.length > 0 && (
              <div className="mb-2">
                <AgentStream
                  status="done"
                  toolSteps={message.toolSteps}
                  currentStep={0}
                  answer=""
                  warning={message.warning || ''}
                />
              </div>
            )}
            <div
              className={`rounded-2xl px-4 py-2.5 text-sm leading-relaxed ${
                isUser
                  ? 'bg-eleball-primary text-white rounded-br-md'
                  : 'bg-eleball-surface-variant text-eleball-text rounded-bl-md'
              }`}
            >
              {isUser ? (
                <UserMessageContent content={message.content} attachments={message.attachments} />
              ) : (
                <AssistantMessageContent
                  content={displayContent}
                  reasoningContent={message.reasoningContent}
                  intermediateContent={message.intermediateContent}
                  loading={loading && isLast && !message.content && !message.reasoningContent && !message.intermediateContent}
                />
              )}
            </div>
          </>
        )}
        {isAssistant && message.resources && message.resources.length > 0 && (
          <div className="mt-2 flex flex-wrap gap-2">
            {message.resources.map((r) => (
              <a
                key={r.resource_id}
                href={agentApi.getResource(r.resource_id)}
                download
                target="_blank"
                rel="noreferrer"
                className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-eleball-surface border border-eleball-outline text-xs text-eleball-text hover:bg-eleball-surface-variant transition-colors"
              >
                <Download className="w-3.5 h-3.5" />
                <span className="max-w-[160px] truncate">{r.file_name || '下载文件'}</span>
              </a>
            ))}
          </div>
        )}
        {isAssistant && message.content && !loading && (
          <CopyButton text={message.content} />
        )}
        {/* AR-12：消息 hover「从此处分叉」按钮，复制到该消息为止的历史到新对话 */}
        {message.id && !loading && onFork && (
          <button
            type="button"
            onClick={() => onFork(message.id, message.sessionId)}
            disabled={forkingMessageId === message.id}
            aria-label="从此处分叉" title="从此处分叉"
            className="absolute -bottom-2 -left-2 p-1 rounded-full bg-eleball-surface border border-eleball-outline-variant text-eleball-text-secondary opacity-0 group-hover:opacity-100 transition-opacity shadow-sm disabled:opacity-50"
          >
            <GitFork className="w-3 h-3" />
          </button>
        )}
      </div>
    </div>
  )
}

function UserMessageContent({ content, attachments = [] }) {
  const text = contentToText(content)
  return (
    <div className="space-y-2">
      {attachments.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {attachments.map((att) =>
            att.type === 'image' ? (
              <img
                key={att.id}
                src={att.dataUrl}
                alt={att.name}
                className="max-w-[120px] max-h-[120px] rounded-lg object-cover border border-white/30"
              />
            ) : (
              <div
                key={att.id}
                className="flex items-center gap-1.5 px-2 py-1 rounded-md bg-white/20 text-white text-xs"
              >
                <FileText className="w-3.5 h-3.5" />
                <span className="max-w-[120px] truncate">{att.name}</span>
              </div>
            )
          )}
        </div>
      )}
      {text && <div className="whitespace-pre-wrap">{text}</div>}
    </div>
  )
}

function AssistantMessageContent({ content, reasoningContent, intermediateContent, loading }) {
  if (loading) {
    return <span className="text-eleball-text-tertiary">思考中…</span>
  }
  if (!content && !reasoningContent && !intermediateContent) return null

  // 按 Markdown 代码块拆分内容，为每个代码块提供复制/下载按钮
  const parts = content ? splitContentByCodeBlocks(content) : []
  return (
    <div className="space-y-3">
      {reasoningContent && (
        <div className="text-xs text-eleball-text-secondary italic border-l-2 border-eleball-text-tertiary pl-2 whitespace-pre-wrap">
          {reasoningContent}
        </div>
      )}
      {intermediateContent && (
        <div className="text-xs text-eleball-text-tertiary border-l-2 border-eleball-outline pl-2 whitespace-pre-wrap">
          {intermediateContent}
        </div>
      )}
      {parts.map((part, idx) =>
        part.type === 'code' ? (
          <CodeBlock key={idx} language={part.language} code={part.code} />
        ) : (
          <div key={idx}>
            <ReactMarkdown
              remarkPlugins={[remarkGfm, remarkBreaks]}
              components={markdownComponents}
            >
              {part.text}
            </ReactMarkdown>
          </div>
        )
      )}
    </div>
  )
}

function CopyButton({ text }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(text)
      } else {
        const ta = document.createElement('textarea')
        ta.value = text
        document.body.appendChild(ta)
        ta.select()
        document.execCommand('copy')
        document.body.removeChild(ta)
      }
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // ignore
    }
  }

  return (
    <button
      onClick={handleCopy}
      className="absolute -bottom-2 -right-2 p-1 rounded-full bg-eleball-surface border border-eleball-outline-variant text-eleball-text-secondary opacity-0 group-hover:opacity-100 transition-opacity shadow-sm"
      aria-label="复制内容" title="复制内容"
    >
      {copied ? (
        <Check className="w-3 h-3 text-eleball-success" />
      ) : (
        <Copy className="w-3 h-3" />
      )}
    </button>
  )
}

function CodeBlock({ language, code }) {
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(code)
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    } catch {
      // ignore
    }
  }

  const handleDownload = () => {
    const filename = suggestedFilename(language, code)
    downloadTextFile(code, filename)
  }

  return (
    <div className="rounded-lg overflow-hidden border border-eleball-outline-variant bg-[#1e1e2e] my-1">
      <div className="flex items-center justify-between px-3 py-1.5 bg-[#252536] text-xs text-gray-400">
        <span className="uppercase">{language || 'code'}</span>
        <div className="flex items-center gap-1">
          <button
            onClick={handleDownload}
            className="p-1 rounded hover:text-white"
            aria-label="下载文件" title="下载文件"
          >
            <Download className="w-3.5 h-3.5" />
          </button>
          <button
            onClick={handleCopy}
            className="p-1 rounded hover:text-white"
            aria-label="复制" title="复制"
          >
            {copied ? <Check className="w-3.5 h-3.5 text-eleball-success" /> : <Copy className="w-3.5 h-3.5" />}
          </button>
        </div>
      </div>
      <pre className="p-3 overflow-x-auto text-sm text-gray-100 font-mono leading-relaxed whitespace-pre-wrap">
        <code>{code}</code>
      </pre>
    </div>
  )
}

const markdownComponents = {
  p: ({ children }) => <p className="mb-2 last:mb-0">{children}</p>,
  h1: ({ children }) => <h1 className="text-xl font-bold mb-2 mt-3">{children}</h1>,
  h2: ({ children }) => <h2 className="text-lg font-bold mb-2 mt-3">{children}</h2>,
  h3: ({ children }) => <h3 className="text-base font-bold mb-1 mt-2">{children}</h3>,
  h4: ({ children }) => <h4 className="text-sm font-semibold mb-1 mt-2">{children}</h4>,
  hr: () => <hr className="my-3 border-eleball-outline-variant" />,
  ul: ({ children }) => <ul className="list-disc pl-5 mb-2 space-y-1">{children}</ul>,
  ol: ({ children }) => <ol className="list-decimal pl-5 mb-2 space-y-1">{children}</ol>,
  li: ({ children }) => <li>{children}</li>,
  table: ({ children }) => (
    <div className="overflow-x-auto rounded-lg border border-eleball-outline my-2">
      <table className="min-w-full text-sm border-collapse">{children}</table>
    </div>
  ),
  thead: ({ children }) => (
    <thead className="bg-eleball-surface-variant text-eleball-text font-semibold">{children}</thead>
  ),
  tbody: ({ children }) => <tbody className="divide-y divide-eleball-outline-variant">{children}</tbody>,
  tr: ({ children }) => <tr className="border-b border-eleball-outline-variant last:border-0">{children}</tr>,
  th: ({ children }) => (
    <th className="px-3 py-2 text-left border-b border-eleball-outline whitespace-pre-wrap">{children}</th>
  ),
  td: ({ children }) => (
    <td className="px-3 py-2 border-b border-eleball-outline-variant last:border-0 whitespace-pre-wrap">{children}</td>
  ),
  strong: ({ children }) => <strong className="font-semibold">{children}</strong>,
  em: ({ children }) => <em className="italic">{children}</em>,
  a: ({ href, children }) => {
    const cleanHref = cleanTrailingPunctuation(href)
    // 对自动链接（文本就是 URL 本身）同步去掉末尾标点，避免把中文括号等包进链接
    const display = typeof children === 'string' && children === href ? cleanTrailingPunctuation(children) : children
    return (
      <a href={cleanHref} target="_blank" rel="noopener noreferrer" className="text-eleball-primary underline break-all">
        {display}
      </a>
    )
  },
  blockquote: ({ children }) => (
    <blockquote className="border-l-4 border-eleball-primary pl-3 italic text-eleball-text-secondary my-2">
      {children}
    </blockquote>
  ),
  code: ({ inline, className, children }) => {
    if (inline) {
      return (
        <code className="bg-gray-100 text-gray-800 px-1 py-0.5 rounded text-xs font-mono">
          {children}
        </code>
      )
    }
    // 兜底：未通过代码块拆分器捕获的围栏代码块
    const lang = (className || '').replace('language-', '')
    return <CodeBlock language={lang} code={String(children).replace(/\n$/, '')} />
  }
}

function splitContentByCodeBlocks(content) {
  const parts = []
  // 允许 opening fence 后没有换行，捕获无语言或单行的代码块
  const regex = /```(\w+)?\n?([\s\S]*?)```/g
  let lastIndex = 0
  let match

  while ((match = regex.exec(content)) !== null) {
    const prefix = content.slice(lastIndex, match.index)
    const language = match[1] || ''
    const code = match[2]
    const trimmedCode = code.trim()

    // 对单行无语言代码块做特殊处理：
    // 1. 紧跟“标注：”后的值；2. 看起来像 URL/域名的内容。
    // 避免把 time.is / www.beijing-time.org 这类值渲染成整段 CODE 块。
    const isInlineValue =
      !language &&
      !trimmedCode.includes('\n') &&
      trimmedCode.length <= 120 &&
      (/[:：]\s*$/.test(prefix) || looksLikeUrlOrDomain(trimmedCode))

    if (isInlineValue) {
      parts.push({ type: 'text', text: prefix + '`' + trimmedCode + '`' })
    } else {
      if (match.index > lastIndex) {
        parts.push({ type: 'text', text: prefix })
      }
      parts.push({ type: 'code', language, code })
    }
    lastIndex = regex.lastIndex
  }

  if (lastIndex < content.length) {
    parts.push({ type: 'text', text: content.slice(lastIndex) })
  }

  if (parts.length === 0) {
    parts.push({ type: 'text', text: content })
  }

  return parts
}

// 去掉 URL 末尾的标点/中文括号，防止自动链接把后面的句子符号也包进去
function cleanTrailingPunctuation(url) {
  if (typeof url !== 'string') return url
  return url.replace(/[.,;:!?\]})£¢€¥」』】）]+$/g, '')
}

// 判断一段文本是否像 URL 或域名（如 time.is / www.beijing-time.org）
function looksLikeUrlOrDomain(text) {
  if (typeof text !== 'string') return false
  const trimmed = text.trim()
  if (/^https?:\/\/\S+$/.test(trimmed)) return true
  return /^[\w-]+(\.[\w-]+)+$/.test(trimmed)
}

// 构造发送给模型接口的历史消息：过滤掉 content 为空的助手消息，
// 避免切换模型后上游因 "assistant message must not be empty" 返回 400
function buildHistoryMessages(messages) {
  return messages
    .filter((m) => !(m.role === 'assistant' && !m.content))
    .map((m) => ({ role: m.role, content: m.content }))
}

function suggestedFilename(language, code) {
  const extMap = {
    javascript: 'snippet.js',
    js: 'snippet.js',
    typescript: 'snippet.ts',
    ts: 'snippet.ts',
    python: 'snippet.py',
    py: 'snippet.py',
    go: 'snippet.go',
    java: 'snippet.java',
    rust: 'snippet.rs',
    rs: 'snippet.rs',
    html: 'snippet.html',
    css: 'snippet.css',
    json: 'data.json',
    csv: 'data.csv',
    xml: 'data.xml',
    yaml: 'data.yaml',
    yml: 'data.yml',
    markdown: 'doc.md',
    md: 'doc.md',
    sql: 'query.sql',
    bash: 'script.sh',
    shell: 'script.sh'
  }

  // 如果内容是合法 JSON，优先按 JSON 处理
  if (safeParseJSON(code)) {
    return 'data.json'
  }

  return extMap[language?.toLowerCase()] || 'snippet.txt'
}
