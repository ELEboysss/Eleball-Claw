import { useState, useEffect } from 'react'
import useSEO from '../hooks/useSEO'
import { useAuth } from '../context/AuthContext'
import { assistantApi, agentMarketApi } from '../api/client'
import { Bot, Plus, Pencil, Trash2, Loader2, Sparkles, ShoppingCart } from 'lucide-react'
import LoginModal from '../components/LoginModal'

// 助手页：助手 = 已激活秘技的命名组合，可在对话页按会话绑定，
// 绑定后 Agent 工作流仅载入该助手包含的秘技工具。
export default function Assistants() {
  useSEO('我的助手', '把已激活的秘技组合成命名助手，在对话中一键应用。')
  const { isLoggedIn } = useAuth()
  const [assistants, setAssistants] = useState([])
  const [loading, setLoading] = useState(true)
  const [message, setMessage] = useState('')
  const [loginOpen, setLoginOpen] = useState(false)
  // 编辑器状态：null 关闭；{ id, name, description, agentIds } 打开（id 为 null 表示新建）
  const [editor, setEditor] = useState(null)
  const [activeAgents, setActiveAgents] = useState([])
  const [candidatesLoading, setCandidatesLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [editorError, setEditorError] = useState('')

  const loadAssistants = () => {
    setLoading(true)
    assistantApi
      .list()
      .then((d) => setAssistants(Array.isArray(d) ? d : d?.items || []))
      .catch((err) => setMessage(err.message || '加载失败'))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    if (!isLoggedIn) return
    loadAssistants()
  }, [isLoggedIn])

  // 候选秘技：我的已购秘技中已激活的部分
  const loadActiveAgents = () => {
    setCandidatesLoading(true)
    agentMarketApi
      .listAgents(1, 100, '', 'hot', 'owned')
      .then((d) => {
        const items = d?.items || d || []
        setActiveAgents(items.filter((a) => a.is_active))
      })
      .catch(() => setActiveAgents([]))
      .finally(() => setCandidatesLoading(false))
  }

  const openCreate = () => {
    setEditorError('')
    setEditor({ id: null, name: '', description: '', agentIds: new Set() })
    loadActiveAgents()
  }

  const openEdit = (assistant) => {
    setEditorError('')
    setEditor({
      id: assistant.id,
      name: assistant.name || '',
      description: assistant.description || '',
      agentIds: new Set((assistant.items || []).map((it) => it.agent_id))
    })
    loadActiveAgents()
  }

  const toggleAgent = (agentId) => {
    setEditor((prev) => {
      const next = new Set(prev.agentIds)
      if (next.has(agentId)) {
        next.delete(agentId)
      } else {
        next.add(agentId)
      }
      return { ...prev, agentIds: next }
    })
  }

  // 保存：新建先 POST 创建再 PUT items；编辑先 PATCH 基本信息再 PUT items
  const handleSave = async () => {
    if (!editor) return
    if (!editor.name.trim()) {
      setEditorError('请填写助手名称')
      return
    }
    setSaving(true)
    setEditorError('')
    try {
      let id = editor.id
      if (id) {
        await assistantApi.update(id, {
          name: editor.name.trim(),
          description: editor.description.trim()
        })
      } else {
        const created = await assistantApi.create({
          name: editor.name.trim(),
          description: editor.description.trim()
        })
        id = created?.id
      }
      await assistantApi.setItems(id, [...editor.agentIds])
      setMessage(editor.id ? `助手「${editor.name.trim()}」已更新` : `助手「${editor.name.trim()}」创建成功`)
      setEditor(null)
      loadAssistants()
    } catch (err) {
      setEditorError(err.message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (assistant) => {
    if (!window.confirm(`确定删除助手「${assistant.name}」吗？该操作不可恢复。`)) return
    setMessage('')
    try {
      await assistantApi.remove(assistant.id)
      setMessage(`助手「${assistant.name}」已删除`)
      loadAssistants()
    } catch (err) {
      setMessage(err.message || '删除失败')
    }
  }

  // 未登录引导
  if (!isLoggedIn) {
    return (
      <div className="pt-24 px-4 text-center min-h-screen">
        <div className="max-w-md mx-auto card">
          <ShoppingCart className="w-12 h-12 mx-auto mb-4 text-eleball-primary" />
          <h2 className="text-xl font-bold text-eleball-text mb-2">登录后管理你的助手</h2>
          <p className="text-sm text-eleball-text-secondary mb-6">
            把已激活的秘技组合成命名助手，在对话中一键应用。
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
    <div className="pt-8 pb-16 px-4 max-w-6xl mx-auto min-h-screen">
      {/* 标题区 */}
      <div className="text-center mb-6">
        <h1 className="text-2xl font-bold text-eleball-text mb-2">我的助手</h1>
        <p className="text-sm text-eleball-text-secondary">
          助手是已激活秘技的命名组合，在对话页绑定后仅载入组合内的工具
        </p>
      </div>

      {/* 新建与消息 */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4 mb-6">
        <button onClick={openCreate} className="btn-primary text-sm px-4 py-2 flex items-center gap-1.5">
          <Plus className="w-4 h-4" />
          新建助手
        </button>
        {message && (
          <div className={`text-sm px-4 py-2 rounded-2xl ${message.includes('成功') || message.includes('已') ? 'bg-emerald-50 text-emerald-600' : 'bg-red-50 text-red-600'}`}>
            {message}
          </div>
        )}
      </div>

      {/* 加载与空态 */}
      {loading && (
        <div className="flex justify-center py-12">
          <Loader2 className="w-8 h-8 animate-spin text-eleball-primary" />
        </div>
      )}
      {!loading && assistants.length === 0 && (
        <div className="text-center py-16 text-eleball-text-secondary">
          <Bot className="w-12 h-12 mx-auto mb-4 opacity-40" />
          <p>还没有助手，点击「新建助手」把已激活的秘技组合起来</p>
        </div>
      )}

      {/* 助手卡片网格 */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
        {assistants.map((assistant) => (
          <div
            key={assistant.id}
            className="card p-5 flex flex-col hover:border-eleball-primary/40 transition-colors"
          >
            <div className="flex items-start justify-between mb-3">
              <div className="w-12 h-12 rounded-2xl bg-eleball-primary-light flex items-center justify-center shrink-0">
                <Bot className="w-6 h-6 text-eleball-primary" />
              </div>
              <div className="flex items-center gap-1">
                <button
                  onClick={() => openEdit(assistant)}
                  className="p-1.5 text-eleball-text-tertiary hover:text-eleball-primary transition-colors"
                  title="编辑"
                >
                  <Pencil className="w-4 h-4" />
                </button>
                <button
                  onClick={() => handleDelete(assistant)}
                  className="p-1.5 text-eleball-text-tertiary hover:text-red-500 transition-colors"
                  title="删除"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </div>

            <h3 className="font-semibold text-eleball-text mb-1">{assistant.name}</h3>
            <p className="text-xs text-eleball-text-secondary line-clamp-2 mb-3">
              {assistant.description || '暂无描述'}
            </p>

            <div className="flex items-center gap-1.5 text-xs text-eleball-text-tertiary mb-3">
              <Sparkles className="w-3 h-3" />
              <span>{(assistant.items || []).length} 个秘技</span>
            </div>
            {(assistant.items || []).length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {assistant.items.map((it) => (
                  <span
                    key={it.agent_id}
                    className="text-xs px-2 py-0.5 rounded-full bg-eleball-surface-variant text-eleball-text-secondary"
                  >
                    {it.name || it.agent_id}
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>

      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />

      {/* 助手编辑弹窗（新建 / 编辑共用） */}
      {editor && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-2xl w-full max-w-md max-h-[85vh] overflow-auto">
            <div className="p-4 border-b border-eleball-outline flex items-center justify-between">
              <h3 className="font-bold text-eleball-text">{editor.id ? '编辑助手' : '新建助手'}</h3>
              <button
                onClick={() => setEditor(null)}
                className="text-eleball-text-tertiary hover:text-eleball-text"
                disabled={saving}
              >
                &times;
              </button>
            </div>
            <div className="p-4 space-y-4">
              {editorError && (
                <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">
                  {editorError}
                </div>
              )}
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1.5">名称</label>
                <input
                  type="text"
                  value={editor.name}
                  onChange={(e) => setEditor((prev) => ({ ...prev, name: e.target.value }))}
                  placeholder="例如：写作助手、调研助手"
                  className="input w-full text-sm"
                  maxLength={50}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1.5">描述</label>
                <textarea
                  value={editor.description}
                  onChange={(e) => setEditor((prev) => ({ ...prev, description: e.target.value }))}
                  placeholder="这个助手用来做什么？（可选）"
                  className="input w-full text-sm h-20 resize-none"
                  maxLength={200}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1.5">
                  选择秘技（仅显示已激活的秘技）
                </label>
                {candidatesLoading ? (
                  <div className="flex justify-center py-6">
                    <Loader2 className="w-5 h-5 animate-spin text-eleball-primary" />
                  </div>
                ) : activeAgents.length === 0 ? (
                  <p className="text-xs text-eleball-text-secondary py-3 text-center">
                    暂无已激活的秘技，请先到技能页激活后再组合
                  </p>
                ) : (
                  <div className="border border-eleball-outline rounded-xl divide-y divide-eleball-outline-variant max-h-56 overflow-auto">
                    {activeAgents.map((agent) => (
                      <label
                        key={agent.id}
                        className="flex items-center gap-3 px-3 py-2.5 cursor-pointer hover:bg-eleball-primary-light/40 transition-colors"
                      >
                        <input
                          type="checkbox"
                          checked={editor.agentIds.has(agent.id)}
                          onChange={() => toggleAgent(agent.id)}
                          className="w-4 h-4 accent-eleball-primary"
                        />
                        <div className="min-w-0">
                          <div className="text-sm text-eleball-text truncate">{agent.name}</div>
                          {agent.description && (
                            <div className="text-xs text-eleball-text-tertiary truncate">{agent.description}</div>
                          )}
                        </div>
                      </label>
                    ))}
                  </div>
                )}
              </div>
              <div className="flex gap-3 pt-1">
                <button
                  onClick={() => setEditor(null)}
                  disabled={saving}
                  className="flex-1 px-4 py-2 rounded-xl text-sm font-medium border border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors disabled:opacity-50"
                >
                  取消
                </button>
                <button
                  onClick={handleSave}
                  disabled={saving}
                  className="flex-1 btn-primary text-sm py-2 justify-center disabled:opacity-50"
                >
                  {saving ? '保存中...' : '保存'}
                </button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
