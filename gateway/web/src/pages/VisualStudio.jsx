import { useEffect, useMemo, useState } from 'react'
import useSEO from '../hooks/useSEO'
import { useSearchParams } from 'react-router-dom'
import { Image as ImageIcon, Film, MessageSquare, Folders } from 'lucide-react'
import VisualCreationInput from '../components/visual/VisualCreationInput'
import VisualChatThread from '../components/visual/VisualChatThread'
import ImagePreviewPanel from '../components/visual/ImagePreviewPanel'
import VideoPreviewPanel from '../components/visual/VideoPreviewPanel'
import VisualConversationList from '../components/visual/VisualConversationList'
import { useVisualTasks } from '../hooks/useVisualTasks'
import { useVisualConversations } from '../hooks/useVisualConversations'
import { isTerminal, MEDIA_TYPES } from '../utils/visualTasks'
import { useAuth } from '../context/AuthContext'
import LoginModal from '../components/LoginModal'
import { publicSettingApi } from '../api/client'

export default function VisualStudio() {
  useSEO('AI 图片/视频生成', '文生图、文生视频，结果转存本地随时查看。Eleball 视觉创作。')
  const { isLoggedIn, user } = useAuth()
  const [searchParams, setSearchParams] = useSearchParams()
  const initialTab = searchParams.get('tab') === 'video' ? 'video' : 'image'
  const [tab, setTab] = useState(initialTab)
  const initialPrompt = searchParams.get('prompt') || ''
  const initialConversation = searchParams.get('conversation') || ''

  const { conversations, loading: convLoading, create: createConversation, update: updateConversation, remove: deleteConversation } = useVisualConversations(tab)
  const [selectedConversationId, setSelectedConversationId] = useState(initialConversation)
  const { tasks, loading: tasksLoading, create, cancel, removeTask } = useVisualTasks(selectedConversationId)
  const [selectedTask, setSelectedTask] = useState(null)
  const [loginOpen, setLoginOpen] = useState(false)
  // AR-13 O12：移动端底部 Tab 切换 会话/对话/预览（< lg 生效，默认对话）
  const [mobilePanel, setMobilePanel] = useState('chat')
  const [globalError, setGlobalError] = useState('')
  const [continuation, setContinuation] = useState(null)
  const [publicSettings, setPublicSettings] = useState({ prompt_fusion_model: '' })

  // 当前选中的模型，用于展示
  const [currentImageModel, setCurrentImageModel] = useState(null)
  const [currentVideoModel, setCurrentVideoModel] = useState(null)

  // 会话列表加载后，默认选中第一个活跃会话；切换 tab 后如果当前会话不在列表中则清空
  useEffect(() => {
    if (conversations.length === 0) {
      setSelectedConversationId('')
      return
    }
    const exists = conversations.some((c) => c.id === selectedConversationId)
    if (!exists) {
      setSelectedConversationId(conversations[0].id)
    }
  }, [conversations, selectedConversationId])

  useEffect(() => {
    setSearchParams({ tab })
  }, [tab, setSearchParams])

  // 切换会话时清空选中的任务，避免预览区显示旧会话成果物
  useEffect(() => {
    setSelectedTask(null)
  }, [selectedConversationId])

  // 加载公开系统设置，用于判断 prompt 融合模型是否已配置
  useEffect(() => {
    let cancelled = false
    publicSettingApi.get().then((data) => {
      if (cancelled) return
      setPublicSettings(data || { prompt_fusion_model: '' })
    }).catch(() => {
      // 忽略公开设置加载失败
    })
    return () => {
      cancelled = true
    }
  }, [])

  const DEFAULT_VISUAL_TITLES = ['新的图片创作', '新的视频创作', '未命名视觉创作']

  const handleCreate = async (payload) => {
    setGlobalError('')
    if (!isLoggedIn) {
      setLoginOpen(true)
      return
    }
    if (!selectedConversationId) {
      setGlobalError('请先选择或创建一个创作会话')
      return
    }
    try {
      const task = await create(tab === 'video' ? MEDIA_TYPES.VIDEO : MEDIA_TYPES.IMAGE, payload)
      // 创建成功后滚动到底部看到新消息，并可选中最新任务
      setSelectedTask(task)
      // 清除接续状态，避免重复预填
      setContinuation(null)

      // 如果会话标题还是默认占位，用 prompt 更新标题
      const conv = conversations.find((c) => c.id === selectedConversationId)
      if (conv && DEFAULT_VISUAL_TITLES.includes(conv.title) && payload.prompt) {
        const newTitle = payload.prompt.slice(0, 20) + (payload.prompt.length > 20 ? '...' : '')
        updateConversation(selectedConversationId, newTitle).catch(() => {
          // 标题更新失败不影响主流程
        })
      }
    } catch (err) {
      setGlobalError(err.message || '创建失败')
    }
  }

  const handleNewConversation = async () => {
    setGlobalError('')
    try {
      const conv = await createConversation(tab === 'video' ? '新的视频创作' : '新的图片创作', tab)
      setSelectedConversationId(conv.id)
    } catch (err) {
      setGlobalError(err.message || '创建会话失败')
    }
  }

  const handleDeleteConversation = async (id) => {
    setGlobalError('')
    try {
      await deleteConversation(id)
      if (selectedConversationId === id) {
        setSelectedConversationId('')
        setSelectedTask(null)
      }
    } catch (err) {
      setGlobalError(err.message || '删除失败')
    }
  }

  const handleCancel = async (id) => {
    setGlobalError('')
    try {
      await cancel(id)
    } catch (err) {
      setGlobalError(err.message || '取消失败')
    }
  }

  const handleContinueTask = (task) => {
    if (!task?.result) return
    let result
    try {
      result = JSON.parse(task.result)
    } catch {
      return
    }
    const isVideo = task.media_type === 'video'
    // 视频连续生成只能使用封面图作为 image 参考，上游（Agnes/Seedance）不接受视频 URL 作为图片参数。
    // 没有封面图时仍允许继续创作，仅把 prompt 带过去，由后端通过 prompt 融合保持上下文。
    const refUrl = isVideo
      ? (result.cover_url || '')
      : (result.url || result.urls?.[0] || '')
    setContinuation({
      prompt: task.prompt || '',
      image_url: refUrl,
      isVideo
    })
    // 点击继续后自动选中该任务，右侧预览区同步展示
    setSelectedTask(task)
  }

  // 始终从 tasks 中查找选中的最新任务，确保轮询状态能同步到预览面板
  const currentTask = useMemo(() => {
    if (!selectedTask) return null
    const mediaType = tab === 'video' ? 'video' : 'image'
    const latest = tasks.find((t) => t.id === selectedTask.id)
    return latest && latest.media_type === mediaType ? latest : null
  }, [selectedTask, tasks, tab])

  // 当前选中的模型
  const currentModel = tab === 'video' ? currentVideoModel : currentImageModel

  // 是否需要显示对话记忆提示：单次任务型协议 + 未配置融合模型 + 当前会话已有任务
  const showMemoryWarning = useMemo(() => {
    const needsFusion = ['agnes_image', 'agnes_video', 'seedance', 'seedream'].includes(currentModel?.protocol)
    return needsFusion && !publicSettings?.prompt_fusion_model && tasks.length > 0
  }, [currentModel, publicSettings, tasks])

  return (
    <div className="h-[calc(100dvh-4rem)] flex flex-col bg-eleball-vs-bg text-eleball-vs-text">
      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />

      {/* 顶部 Tab */}
      <div className="border-b border-eleball-vs-border px-4 py-3 bg-eleball-vs-surface">
        <div className="mx-auto w-full max-w-[1280px] flex items-center gap-4">
          <h1 className="text-lg font-semibold text-eleball-vs-text mr-4">视觉工作室</h1>
          <button
            onClick={() => setTab('image')}
            className={`flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'image'
                ? 'bg-eleball-primary text-white'
                : 'bg-eleball-vs-surface-variant text-eleball-vs-text-muted hover:bg-eleball-primary/20 hover:text-eleball-vs-accent'
            }`}
          >
            <ImageIcon className="w-4 h-4" />
            图片生成
          </button>
          <button
            onClick={() => setTab('video')}
            className={`flex items-center gap-2 rounded-lg px-4 py-2 text-sm font-medium transition-colors ${
              tab === 'video'
                ? 'bg-eleball-primary text-white'
                : 'bg-eleball-vs-surface-variant text-eleball-vs-text-muted hover:bg-eleball-primary/20 hover:text-eleball-vs-accent'
            }`}
          >
            <Film className="w-4 h-4" />
            视频生成
          </button>
        </div>
      </div>

      {globalError && (
        <div className="mx-auto w-full max-w-[1280px] px-4 pt-3">
          <div className="rounded-lg bg-red-900/30 border border-red-800/50 px-4 py-2 text-sm text-red-300">
            {globalError}
          </div>
        </div>
      )}

      {/* 主内容区 */}
      <div className="flex-1 min-h-0 overflow-hidden">
        <div className="mx-auto w-full max-w-[1280px] h-full p-4">
          <div className="grid h-full grid-cols-1 grid-rows-1 gap-4 lg:grid-cols-12">
            {/* 最左侧：会话列表 */}
            <div className={`${mobilePanel === 'list' ? 'flex' : 'hidden'} lg:flex lg:col-span-2 flex-col min-h-0`}>
              <VisualConversationList
                conversations={conversations}
                selectedId={selectedConversationId}
                onSelect={setSelectedConversationId}
                onCreate={handleNewConversation}
                onDelete={handleDeleteConversation}
                mediaType={tab}
              />
            </div>

            {/* 中间：会话记录流 + 浮动创作输入区 */}
            <div className={`${mobilePanel === 'chat' ? 'flex' : 'hidden'} lg:flex lg:col-span-5 flex-col rounded-xl border border-eleball-vs-border bg-eleball-vs-surface shadow-sm overflow-hidden relative`}>
              <div className="flex-1 min-h-0 overflow-hidden">
                <VisualChatThread
                  tasks={tasks.filter((t) => t.media_type === (tab === 'video' ? 'video' : 'image'))}
                  selectedTask={currentTask}
                  onSelectTask={setSelectedTask}
                  onContinueTask={handleContinueTask}
                  showMemoryWarning={showMemoryWarning}
                  mediaType={tab}
                />
              </div>
              <VisualCreationInput
                tab={tab}
                disabled={currentTask?.media_type === (tab === 'video' ? 'video' : 'image') && !isTerminal(currentTask.status)}
                onCreate={handleCreate}
                onModelChange={tab === 'video' ? setCurrentVideoModel : setCurrentImageModel}
                initialPrompt={initialPrompt}
                continuation={continuation}
              />
            </div>

            {/* 右侧：选中任务预览/详情 */}
            <div className={`${mobilePanel === 'preview' ? 'flex' : 'hidden'} lg:flex lg:col-span-5 flex-col rounded-xl border border-eleball-vs-border bg-eleball-vs-surface p-4 shadow-sm`}>
              {tab === 'video' ? (
                <VideoPreviewPanel task={currentTask} />
              ) : (
                <ImagePreviewPanel task={currentTask} />
              )}
            </div>
          </div>
        </div>
      </div>

      {/* AR-13 O12：移动端底部 Tab 切换 会话/对话/预览（< lg） */}
      <div className="lg:hidden flex shrink-0 border-t border-eleball-vs-border bg-eleball-vs-surface">
        <button
          onClick={() => setMobilePanel('list')}
          className={`flex-1 flex items-center justify-center gap-1 py-2.5 text-xs font-medium transition-colors ${mobilePanel === 'list' ? 'text-eleball-vs-accent' : 'text-eleball-vs-text-muted'}`}
        >
          <Folders className="w-4 h-4" />会话
        </button>
        <button
          onClick={() => setMobilePanel('chat')}
          className={`flex-1 flex items-center justify-center gap-1 py-2.5 text-xs font-medium transition-colors ${mobilePanel === 'chat' ? 'text-eleball-vs-accent' : 'text-eleball-vs-text-muted'}`}
        >
          <MessageSquare className="w-4 h-4" />对话
        </button>
        <button
          onClick={() => setMobilePanel('preview')}
          className={`flex-1 flex items-center justify-center gap-1 py-2.5 text-xs font-medium transition-colors ${mobilePanel === 'preview' ? 'text-eleball-vs-accent' : 'text-eleball-vs-text-muted'}`}
        >
          <ImageIcon className="w-4 h-4" />预览
        </button>
      </div>
    </div>
  )
}
