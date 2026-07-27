import { useState } from 'react'
import { teamApi } from '../api/client'
import { Plus, Pencil, Trash2, X, Folders } from 'lucide-react'

// 分组管理弹窗（Agent Team）：列出当前用户的对话分组，支持新建 / 改名 / 改描述 / 删除。
// 删除分组仅清空组内对话的 team_id 与组共享记忆，不删除对话本身。
// 组列表与变更由父组件持有（teams + onTeamsChange），本组件只负责交互与调用 teamApi。
export default function TeamModal({ open, onClose, teams, onTeamsChange }) {
  // 编辑器：null 关闭；{ id, name, description } 打开（id 为 null 表示新建）
  const [editor, setEditor] = useState(null)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  if (!open) return null

  const openCreate = () => {
    setError('')
    setEditor({ id: null, name: '', description: '' })
  }

  const openEdit = (team) => {
    setError('')
    setEditor({ id: team.id, name: team.name || '', description: team.description || '' })
  }

  const handleSave = async () => {
    if (!editor) return
    if (!editor.name.trim()) {
      setError('请填写组名称')
      return
    }
    setSaving(true)
    setError('')
    try {
      const payload = { name: editor.name.trim(), description: editor.description.trim() }
      if (editor.id) {
        await teamApi.update(editor.id, payload)
      } else {
        await teamApi.create(payload)
      }
      setEditor(null)
      onTeamsChange?.()
    } catch (err) {
      setError(err.message || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (team) => {
    if (!window.confirm(`确定删除分组「${team.name}」吗？\n组内对话不会被删除（仅移出分组），但组共享记忆将被清除。`)) return
    setError('')
    try {
      await teamApi.remove(team.id)
      onTeamsChange?.()
    } catch (err) {
      setError(err.message || '删除失败')
    }
  }

  return (
    <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
      <div className="bg-white rounded-2xl w-full max-w-md max-h-[85vh] overflow-auto">
        <div className="p-4 border-b border-eleball-outline flex items-center justify-between">
          <h3 className="font-bold text-eleball-text flex items-center gap-2">
            <Folders className="w-4 h-4 text-eleball-primary" />
            分组管理
          </h3>
          <button
            onClick={onClose}
            className="text-eleball-text-tertiary hover:text-eleball-text"
            disabled={saving}
          >
            &times;
          </button>
        </div>
        <div className="p-4 space-y-4">
          {error && (
            <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{error}</div>
          )}

          {/* 分组列表 */}
          {(!teams || teams.length === 0) && !editor && (
            <p className="text-sm text-eleball-text-secondary text-center py-6">
              还没有分组，点击下方「新建分组」创建一个。
            </p>
          )}
          {teams && teams.length > 0 && (
            <div className="space-y-1.5">
              {teams.map((team) => (
                <div
                  key={team.id}
                  className="group flex items-center gap-2 px-3 py-2 rounded-xl border border-eleball-outline-variant hover:bg-eleball-surface-variant/50 transition-colors"
                >
                  <div className="flex-1 min-w-0">
                    <p className="text-sm text-eleball-text truncate">{team.name}</p>
                    {team.description && (
                      <p className="text-xs text-eleball-text-tertiary truncate">{team.description}</p>
                    )}
                  </div>
                  <span className="text-xs text-eleball-text-tertiary shrink-0">
                    {team.conversation_count ?? 0} 对话
                  </span>
                  <button
                    onClick={() => openEdit(team)}
                    className="p-1 text-eleball-text-tertiary hover:text-eleball-primary transition-colors"
                    title="编辑"
                  >
                    <Pencil className="w-3.5 h-3.5" />
                  </button>
                  <button
                    onClick={() => handleDelete(team)}
                    className="p-1 text-eleball-text-tertiary hover:text-red-500 transition-colors"
                    title="删除分组"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </button>
                </div>
              ))}
            </div>
          )}

          {/* 新建 / 编辑表单 */}
          {editor ? (
            <div className="space-y-3 p-3 rounded-xl bg-eleball-surface-variant/40 border border-eleball-outline-variant">
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1.5">组名称</label>
                <input
                  type="text"
                  value={editor.name}
                  onChange={(e) => setEditor((prev) => ({ ...prev, name: e.target.value }))}
                  placeholder="例如：写作小组、调研项目"
                  className="input w-full text-sm"
                  maxLength={128}
                  autoFocus
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-eleball-text mb-1.5">描述（可选）</label>
                <textarea
                  value={editor.description}
                  onChange={(e) => setEditor((prev) => ({ ...prev, description: e.target.value }))}
                  placeholder="这个分组用来做什么？"
                  className="input w-full text-sm h-16 resize-none"
                  maxLength={500}
                />
              </div>
              <div className="flex gap-3">
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
          ) : (
            <button
              onClick={openCreate}
              className="btn-primary text-sm px-4 py-2 flex items-center gap-1.5"
            >
              <Plus className="w-4 h-4" />
              新建分组
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
