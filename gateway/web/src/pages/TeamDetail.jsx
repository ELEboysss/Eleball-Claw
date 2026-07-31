import { useState, useEffect, useMemo, useCallback } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import useSEO from '../hooks/useSEO'
import { useAuth } from '../context/AuthContext'
import { useChat } from '../context/ChatContext'
import { teamApi, assistantApi } from '../api/client'
import {
  ArrowLeft,
  Pencil,
  Trash2,
  Plus,
  MessageSquare,
  Brain,
  Share2,
  Folder,
  Loader2
} from 'lucide-react'

// 分组详情页（Agent Team P4）：
// - 组内对话：点击跳回对话页并选中（同一 ChatContext）
// - 组共享记忆：列表 / 删除 / 手动新增
// - 能力目录预览：当前用户 shared 且（全局 或 本组）的助手，即可被本组编排者经 CallAssistant 委派的助手
export default function TeamDetail() {
  const { teamId } = useParams()
  const navigate = useNavigate()
  const { isLoggedIn } = useAuth()
  const { setCurrentConversationId } = useChat()
  useSEO('分组详情', '对话分组的对话、共享记忆与能力目录')

  const [team, setTeam] = useState(null)
  const [memories, setMemories] = useState([])
  const [assistants, setAssistants] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editMode, setEditMode] = useState(false)
  const [editForm, setEditForm] = useState({ name: '', description: '' })
  const [saving, setSaving] = useState(false)
  const [newMemory, setNewMemory] = useState({ content: '', tags: '' })
  const [memorySaving, setMemorySaving] = useState(false)
  const [memoryError, setMemoryError] = useState('')

  const loadAll = useCallback(async () => {
    if (!teamId) return
    setLoading(true)
    setError('')
    try {
      const [detail, memRes, asstRes] = await Promise.all([
        teamApi.get(teamId),
        teamApi.listMemories(teamId),
        assistantApi.list()
      ])
      setTeam(detail)
      setMemories(memRes?.items || [])
      setAssistants(Array.isArray(asstRes) ? asstRes : asstRes?.items || [])
    } catch (err) {
      setError(err.message || '加载分组失败')
    } finally {
      setLoading(false)
    }
  }, [teamId])

  useEffect(() => {
    loadAll()
  }, [loadAll])

  // 能力目录：shared 且（全局可见 或 本组专属）的助手
  const capabilityDir = useMemo(
    () => assistants.filter((a) => a.shared && (!a.team_id || a.team_id === teamId)),
    [assistants, teamId]
  )

  const startEdit = () => {
    setEditForm({ name: team?.name || '', description: team?.description || '' })
    setEditMode(true)
  }

  const handleSaveTeam = async () => {
    if (!editForm.name.trim()) {
      setError('组名称不能为空')
      return
    }
    setSaving(true)
    setError('')
    try {
      await teamApi.update(teamId, {
        name: editForm.name.trim(),
        description: editForm.description.trim()
      })
      setEditMode(false)
      await loadAll()
    } catch (err) {
      setError(err.message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const handleDeleteMemory = async (memoryId) => {
    if (!window.confirm('确定删除这条组记忆吗？')) return
    try {
      await teamApi.removeMemory(teamId, memoryId)
      setMemories((prev) => prev.filter((m) => m.id !== memoryId))
    } catch (err) {
      setMemoryError(err.message || '删除失败')
    }
  }

  const handleAddMemory = async () => {
    if (!newMemory.content.trim()) {
      setMemoryError('请填写记忆内容')
      return
    }
    setMemorySaving(true)
    setMemoryError('')
    try {
      const created = await teamApi.createMemory(teamId, {
        content: newMemory.content.trim(),
        tags: newMemory.tags.trim()
      })
      setMemories((prev) => [created, ...prev])
      setNewMemory({ content: '', tags: '' })
    } catch (err) {
      setMemoryError(err.message || '添加失败')
    } finally {
      setMemorySaving(false)
    }
  }

  const openConversation = (convId) => {
    setCurrentConversationId(convId)
    navigate('/chat')
  }

  if (!isLoggedIn) {
    return (
      <div className="pt-24 px-4 text-center min-h-screen">
        <div className="max-w-md mx-auto card p-8">
          <p className="text-eleball-text-secondary mb-4">请先登录后查看分组</p>
          <button onClick={() => navigate('/')} className="btn-primary text-sm px-4 py-2">
            返回首页
          </button>
        </div>
      </div>
    )
  }

  if (loading) {
    return (
      <div className="pt-24 flex justify-center">
        <Loader2 className="w-8 h-8 animate-spin text-eleball-primary" />
      </div>
    )
  }

  if (error && !team) {
    return (
      <div className="pt-24 px-4 text-center min-h-screen">
        <div className="max-w-md mx-auto card p-8">
          <p className="text-red-600 mb-4">{error}</p>
          <button onClick={() => navigate('/chat')} className="btn-primary text-sm px-4 py-2">
            返回对话
          </button>
        </div>
      </div>
    )
  }

  const conversations = team?.conversations || []

  return (
    <div className="pt-8 pb-16 px-4 max-w-4xl mx-auto min-h-screen">
      <button
        onClick={() => navigate('/chat')}
        className="flex items-center gap-1 text-sm text-eleball-text-secondary hover:text-eleball-text mb-4 transition-colors"
      >
        <ArrowLeft className="w-4 h-4" />
        返回对话
      </button>

      {/* 分组头部 */}
      <div className="card p-5 mb-6">
        {editMode ? (
          <div className="space-y-3">
            {error && (
              <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{error}</div>
            )}
            <div>
              <label className="block text-sm font-medium text-eleball-text mb-1.5">组名称</label>
              <input
                type="text"
                value={editForm.name}
                onChange={(e) => setEditForm((prev) => ({ ...prev, name: e.target.value }))}
                className="input w-full text-sm"
                maxLength={128}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-eleball-text mb-1.5">描述</label>
              <textarea
                value={editForm.description}
                onChange={(e) => setEditForm((prev) => ({ ...prev, description: e.target.value }))}
                className="input w-full text-sm h-16 resize-none"
                maxLength={500}
              />
            </div>
            <div className="flex gap-3">
              <button
                onClick={() => setEditMode(false)}
                disabled={saving}
                className="flex-1 px-4 py-2 rounded-xl text-sm font-medium border border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors disabled:opacity-50"
              >
                取消
              </button>
              <button
                onClick={handleSaveTeam}
                disabled={saving}
                className="flex-1 btn-primary text-sm py-2 justify-center disabled:opacity-50"
              >
                {saving ? '保存中...' : '保存'}
              </button>
            </div>
          </div>
        ) : (
          <div>
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <h1 className="text-xl font-bold text-eleball-text flex items-center gap-2">
                  <Folder className="w-5 h-5 text-eleball-primary flex-shrink-0" />
                  <span className="truncate">{team?.name}</span>
                </h1>
                {team?.description && (
                  <p className="text-sm text-eleball-text-secondary mt-1.5">{team.description}</p>
                )}
              </div>
              <button
                onClick={startEdit}
                className="p-1.5 text-eleball-text-tertiary hover:text-eleball-primary transition-colors flex-shrink-0"
                title="编辑分组"
              >
                <Pencil className="w-4 h-4" />
              </button>
            </div>
            <div className="flex items-center gap-4 mt-3 text-xs text-eleball-text-tertiary">
              <span className="flex items-center gap-1">
                <MessageSquare className="w-3 h-3" />
                {conversations.length} 对话
              </span>
              <span className="flex items-center gap-1">
                <Brain className="w-3 h-3" />
                {memories.length} 记忆
              </span>
              <span className="flex items-center gap-1">
                <Share2 className="w-3 h-3" />
                {capabilityDir.length} 可委派助手
              </span>
            </div>
          </div>
        )}
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* 组内对话 */}
        <div className="card p-5">
          <h2 className="font-semibold text-eleball-text mb-3 flex items-center gap-2">
            <MessageSquare className="w-4 h-4 text-eleball-primary" />
            组内对话
          </h2>
          {conversations.length === 0 ? (
            <p className="text-sm text-eleball-text-tertiary text-center py-6">该分组暂无对话</p>
          ) : (
            <div className="space-y-1.5">
              {conversations.map((conv) => (
                <button
                  key={conv.id}
                  onClick={() => openConversation(conv.id)}
                  className="group flex items-center gap-2 w-full text-left px-3 py-2 rounded-xl hover:bg-eleball-primary-light transition-colors"
                >
                  <MessageSquare className="w-4 h-4 text-eleball-text-tertiary flex-shrink-0" />
                  <span className="flex-1 min-w-0 text-sm text-eleball-text truncate">
                    {conv.title || '新对话'}
                  </span>
                  {conv.model && (
                    <span className="text-[10px] text-eleball-text-tertiary bg-eleball-surface-variant px-1.5 py-0.5 rounded-full flex-shrink-0">
                      {conv.model}
                    </span>
                  )}
                </button>
              ))}
            </div>
          )}
        </div>

        {/* 组共享记忆 */}
        <div className="card p-5">
          <h2 className="font-semibold text-eleball-text mb-3 flex items-center gap-2">
            <Brain className="w-4 h-4 text-eleball-primary" />
            组共享记忆
          </h2>
          {/* 新增记忆 */}
          <div className="mb-3 p-3 rounded-xl bg-eleball-surface-variant/40 border border-eleball-outline-variant space-y-2">
            <textarea
              value={newMemory.content}
              onChange={(e) => setNewMemory((prev) => ({ ...prev, content: e.target.value }))}
              placeholder="沉淀一条事实/偏好/结论（≤500 字）"
              className="input w-full text-sm h-14 resize-none"
              maxLength={500}
            />
            <div className="flex gap-2">
              <input
                type="text"
                value={newMemory.tags}
                onChange={(e) => setNewMemory((prev) => ({ ...prev, tags: e.target.value }))}
                placeholder="标签，逗号分隔（可选）"
                className="input flex-1 text-sm"
                maxLength={256}
              />
              <button
                onClick={handleAddMemory}
                disabled={memorySaving}
                className="btn-primary text-sm px-3 py-2 flex items-center gap-1 disabled:opacity-50"
              >
                <Plus className="w-4 h-4" />
                添加
              </button>
            </div>
            {memoryError && (
              <p className="text-xs text-red-600">{memoryError}</p>
            )}
          </div>
          {memories.length === 0 ? (
            <p className="text-sm text-eleball-text-tertiary text-center py-6">
              暂无组记忆，对话中的事实会自动沉淀，也可手动添加
            </p>
          ) : (
            <div className="space-y-2 max-h-80 overflow-auto">
              {memories.map((m) => (
                <div
                  key={m.id}
                  className="group flex items-start gap-2 px-3 py-2 rounded-xl border border-eleball-outline-variant"
                >
                  <div className="flex-1 min-w-0">
                    <p className="text-sm text-eleball-text break-words">{m.content}</p>
                    {m.tags && (
                      <div className="flex flex-wrap gap-1 mt-1">
                        {m.tags.split(',').filter(Boolean).map((tag, i) => (
                          <span
                            key={i}
                            className="text-[10px] px-1.5 py-0.5 rounded-full bg-eleball-surface-variant text-eleball-text-secondary"
                          >
                            {tag.trim()}
                          </span>
                        ))}
                      </div>
                    )}
                  </div>
                  <button
                    onClick={() => handleDeleteMemory(m.id)}
                    className="p-1 text-eleball-text-tertiary hover:text-red-500 transition-opacity opacity-0 group-hover:opacity-100 focus-visible:opacity-100 flex-shrink-0"
                    title="删除记忆"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 能力目录预览 */}
      <div className="card p-5 mt-6">
        <h2 className="font-semibold text-eleball-text mb-1 flex items-center gap-2">
          <Share2 className="w-4 h-4 text-eleball-primary" />
          能力目录预览
        </h2>
        <p className="text-xs text-eleball-text-tertiary mb-3">
          以下助手对本组编排者可见，可经 CallAssistant 工具委派子任务（全局共享 + 本组专属）。
        </p>
        {capabilityDir.length === 0 ? (
          <p className="text-sm text-eleball-text-tertiary text-center py-6">
            暂无可委派助手，到「我的助手」开启「对编排者共享」即可加入目录
          </p>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
            {capabilityDir.map((a) => (
              <div
                key={a.id}
                className="p-3 rounded-xl border border-eleball-outline-variant hover:border-eleball-primary/40 transition-colors"
              >
                <div className="flex items-center gap-2 mb-1">
                  <span className="text-sm font-medium text-eleball-text truncate">{a.name}</span>
                  {!a.team_id && (
                    <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-purple-50 text-purple-600 flex-shrink-0">
                      全局
                    </span>
                  )}
                </div>
                {a.description && (
                  <p className="text-xs text-eleball-text-secondary line-clamp-2 mb-1.5">
                    {a.description}
                  </p>
                )}
                {(a.items || []).length > 0 && (
                  <div className="flex flex-wrap gap-1">
                    {a.items.slice(0, 4).map((it) => (
                      <span
                        key={it.agent_id}
                        className="text-[10px] px-1.5 py-0.5 rounded-full bg-eleball-surface-variant text-eleball-text-secondary"
                      >
                        {it.name || it.agent_id}
                      </span>
                    ))}
                    {a.items.length > 4 && (
                      <span className="text-[10px] text-eleball-text-tertiary">+{a.items.length - 4}</span>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
