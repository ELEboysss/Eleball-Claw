import { useState, useEffect } from 'react'
import { agentApi } from '../api/client'

const statusOptions = [
  { value: '', label: '全部' },
  { value: 'pending', label: '待审核' },
  { value: 'approved', label: '已通过' },
  { value: 'rejected', label: '已拒绝' },
  { value: 'delisted', label: '已下架' },
]

const statusBadge = {
  pending: { text: '待审核', className: 'bg-yellow-100 text-yellow-800' },
  approved: { text: '已通过', className: 'bg-green-100 text-green-800' },
  rejected: { text: '已拒绝', className: 'bg-red-100 text-red-800' },
  delisted: { text: '已下架', className: 'bg-gray-100 text-gray-800' },
}

const levelMap = {
  1: '黄阶',
  2: '玄阶',
  3: '地阶',
  4: '天阶',
  5: '仙阶',
  6: '焚决',
}

function parseManifest(json) {
  if (!json) return null
  try {
    return JSON.parse(json)
  } catch {
    return null
  }
}

function AgentsReview() {
  const [agents, setAgents] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize] = useState(10)
  const [statusFilter, setStatusFilter] = useState('pending')
  const [loading, setLoading] = useState(false)
  const [detailModal, setDetailModal] = useState(null)
  const [reviewModal, setReviewModal] = useState(null)
  const [adminNote, setAdminNote] = useState('')
  const [actionLoading, setActionLoading] = useState(false)
  const [depStatus, setDepStatus] = useState(null)
  const [depLoading, setDepLoading] = useState(false)
  const [approveResult, setApproveResult] = useState(null)

  const fetchAgents = async () => {
    setLoading(true)
    try {
      const res = await agentApi.listForReview(page, pageSize, statusFilter)
      setAgents(res.data?.items || [])
      setTotal(res.data?.total || 0)
    } catch (err) {
      alert('加载失败: ' + err)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchAgents()
  }, [page, statusFilter])

  const fetchDependencies = async (id) => {
    setDepLoading(true)
    try {
      const res = await agentApi.dependencies(id)
      setDepStatus(res.data || res)
    } catch (err) {
      setDepStatus(null)
    } finally {
      setDepLoading(false)
    }
  }

  const handleApprove = async (id) => {
    if (!window.confirm('确认通过该秘技上架？')) return
    setActionLoading(true)
    try {
      const res = await agentApi.approve(id, adminNote)
      setReviewModal(null)
      setAdminNote('')
      setDepStatus(null)
      setApproveResult(res.data || null)
      fetchAgents()
    } catch (err) {
      alert('操作失败: ' + err)
    } finally {
      setActionLoading(false)
    }
  }

  const handleReject = async (id) => {
    const note = window.prompt('请输入拒绝原因（可选）：')
    if (note === null) return
    setActionLoading(true)
    try {
      await agentApi.reject(id, note)
      fetchAgents()
    } catch (err) {
      alert('操作失败: ' + err)
    } finally {
      setActionLoading(false)
    }
  }

  const handleDelist = async (id) => {
    if (!window.confirm('确认下架该秘技？下架后将无法被购买。')) return
    setActionLoading(true)
    try {
      await agentApi.delist(id)
      fetchAgents()
    } catch (err) {
      alert('操作失败: ' + err)
    } finally {
      setActionLoading(false)
    }
  }

  const totalPages = Math.ceil(total / pageSize)

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-eleball-text">秘技审核</h1>
        <div className="flex items-center gap-3">
          <select
            value={statusFilter}
            onChange={(e) => { setStatusFilter(e.target.value); setPage(1) }}
            className="px-3 py-2 rounded-xl border border-eleball-outline text-sm focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
          >
            {statusOptions.map((opt) => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
          <button
            onClick={fetchAgents}
            className="px-4 py-2 rounded-xl bg-eleball-primary text-white text-sm font-medium hover:bg-eleball-primary-dark transition-colors"
          >
            刷新
          </button>
        </div>
      </div>

      {approveResult && (
        <div className="mb-6 rounded-2xl border border-green-200 bg-green-50 p-4">
          <div className="flex items-start justify-between">
            <div>
              <p className="text-sm font-medium text-green-800">审批通过，已自动创建/更新驱动别名</p>
              <p className="mt-1 text-xs text-green-700">请将下方注册令牌交给开发者，用于自助注册模块服务。</p>
              <div className="mt-2 space-y-1 text-sm">
                <div><span className="text-green-700">驱动别名：</span><span className="font-mono font-medium">{approveResult.driver_id}</span></div>
                <div className="flex items-center gap-2">
                  <span className="text-green-700">注册令牌：</span>
                  <code className="rounded bg-white px-2 py-0.5 font-mono text-green-800 border border-green-200">{approveResult.auth_token}</code>
                </div>
              </div>
            </div>
            <button onClick={() => setApproveResult(null)} className="text-green-700 hover:text-green-900">&times;</button>
          </div>
        </div>
      )}

      {/* 统计卡片 */}
      <div className="grid grid-cols-4 gap-4 mb-6">
        {statusOptions.filter(s => s.value).map((opt) => {
          const count = agents.filter(a => a.status === opt.value).length
          return (
            <div key={opt.value} className="bg-white rounded-2xl p-4 border border-eleball-outline">
              <p className="text-xs text-eleball-text-secondary mb-1">{opt.label}</p>
              <p className="text-xl font-bold text-eleball-text">{opt.value === statusFilter ? total : '-'}</p>
            </div>
          )
        })}
      </div>

      {/* 列表 */}
      <div className="bg-white rounded-2xl border border-eleball-outline overflow-hidden">
        <table className="w-full text-sm">
          <thead className="bg-gray-50 border-b border-eleball-outline">
            <tr>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">秘技名称</th>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">开发者</th>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">分类</th>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">等级</th>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">价格</th>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">状态</th>
              <th className="text-left px-6 py-3 font-medium text-eleball-text-secondary">提交时间</th>
              <th className="text-right px-6 py-3 font-medium text-eleball-text-secondary">操作</th>
            </tr>
          </thead>
          <tbody>
            {loading ? (
              <tr><td colSpan={8} className="px-6 py-12 text-center text-eleball-text-secondary">加载中...</td></tr>
            ) : agents.length === 0 ? (
              <tr><td colSpan={8} className="px-6 py-12 text-center text-eleball-text-secondary">暂无数据</td></tr>
            ) : (
              agents.map((agent) => {
                const badge = statusBadge[agent.status] || statusBadge.pending
                return (
                  <tr key={agent.id} className="border-b border-eleball-outline last:border-0 hover:bg-gray-50">
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-2">
                        <div className="font-medium text-eleball-text">{agent.name}</div>
                        {parseManifest(agent.manifest_json)?.driver && (
                          <span className="px-1.5 py-0.5 rounded text-[10px] bg-blue-50 text-blue-700 border border-blue-100">
                            {parseManifest(agent.manifest_json).driver}
                          </span>
                        )}
                      </div>
                      <div className="text-xs text-eleball-text-secondary truncate max-w-[200px]">{agent.description}</div>
                    </td>
                    <td className="px-6 py-4">{agent.creator_name || agent.creator_id}</td>
                    <td className="px-6 py-4">{agent.category}</td>
                    <td className="px-6 py-4">{levelMap[agent.level] || '未知'}</td>
                    <td className="px-6 py-4">
                      {agent.price_danwan > 0 ? `${agent.price_danwan} 弹丸` : '免费'}
                      {agent.price_elegant ? ` / ${agent.price_elegant} 优雅弹丸` : ''}
                    </td>
                    <td className="px-6 py-4">
                      <span className={`px-2 py-1 rounded-lg text-xs font-medium ${badge.className}`}>
                        {badge.text}
                      </span>
                    </td>
                    <td className="px-6 py-4 text-eleball-text-secondary">
                      {new Date(agent.created_at).toLocaleDateString('zh-CN')}
                    </td>
                    <td className="px-6 py-4 text-right">
                      <div className="flex items-center justify-end gap-2">
                        <button
                          onClick={() => { setDetailModal(agent); fetchDependencies(agent.id) }}
                          className="px-3 py-1.5 rounded-lg text-xs font-medium text-eleball-primary hover:bg-eleball-primary/10 transition-colors"
                        >
                          详情
                        </button>
                        {agent.status === 'pending' && (
                          <>
                            <button
                              onClick={() => handleApprove(agent.id)}
                              disabled={actionLoading}
                              className="px-3 py-1.5 rounded-lg text-xs font-medium bg-green-50 text-green-700 hover:bg-green-100 transition-colors disabled:opacity-50"
                            >
                              通过
                            </button>
                            <button
                              onClick={() => handleReject(agent.id)}
                              disabled={actionLoading}
                              className="px-3 py-1.5 rounded-lg text-xs font-medium bg-red-50 text-red-700 hover:bg-red-100 transition-colors disabled:opacity-50"
                            >
                              拒绝
                            </button>
                          </>
                        )}
                        {agent.status === 'approved' && (
                          <button
                            onClick={() => handleDelist(agent.id)}
                            disabled={actionLoading}
                            className="px-3 py-1.5 rounded-lg text-xs font-medium bg-gray-50 text-gray-700 hover:bg-gray-100 transition-colors disabled:opacity-50"
                          >
                            下架
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>

        {/* 分页 */}
        {totalPages > 1 && (
          <div className="flex items-center justify-between px-6 py-4 border-t border-eleball-outline">
            <span className="text-sm text-eleball-text-secondary">
              共 {total} 条，第 {page}/{totalPages} 页
            </span>
            <div className="flex gap-2">
              <button
                onClick={() => setPage(p => Math.max(1, p - 1))}
                disabled={page === 1}
                className="px-3 py-1.5 rounded-lg text-sm border border-eleball-outline hover:bg-gray-50 disabled:opacity-50"
              >
                上一页
              </button>
              <button
                onClick={() => setPage(p => Math.min(totalPages, p + 1))}
                disabled={page === totalPages}
                className="px-3 py-1.5 rounded-lg text-sm border border-eleball-outline hover:bg-gray-50 disabled:opacity-50"
              >
                下一页
              </button>
            </div>
          </div>
        )}
      </div>

      {/* 详情弹窗 */}
      {detailModal && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-50 p-4">
          <div className="bg-white rounded-2xl w-full max-w-lg max-h-[80vh] overflow-auto">
            <div className="p-6 border-b border-eleball-outline flex items-center justify-between">
              <h3 className="text-lg font-bold">秘技详情</h3>
              <button onClick={() => { setDetailModal(null); setDepStatus(null) }} className="text-2xl text-eleball-text-secondary hover:text-eleball-text">&times;</button>
            </div>
            <div className="p-6 space-y-4 text-sm">
              <div><span className="text-eleball-text-secondary">ID：</span>{detailModal.id}</div>
              <div><span className="text-eleball-text-secondary">名称：</span>{detailModal.name}</div>
              <div><span className="text-eleball-text-secondary">开发者：</span>{detailModal.creator_name || detailModal.creator_id}</div>
              <div><span className="text-eleball-text-secondary">分类：</span>{detailModal.category}</div>
              <div><span className="text-eleball-text-secondary">等级：</span>{levelMap[detailModal.level] || '未知'}</div>
              <div><span className="text-eleball-text-secondary">价格：</span>
                {detailModal.price_danwan > 0 ? `${detailModal.price_danwan} 弹丸` : '免费'}
                {detailModal.price_elegant ? ` / ${detailModal.price_elegant} 优雅弹丸` : ''}
              </div>
              <div><span className="text-eleball-text-secondary">状态：</span>
                <span className={`px-2 py-0.5 rounded text-xs font-medium ${statusBadge[detailModal.status]?.className}`}>
                  {statusBadge[detailModal.status]?.text}
                </span>
              </div>
              <div><span className="text-eleball-text-secondary">描述：</span>
                <p className="mt-1 p-3 bg-gray-50 rounded-xl">{detailModal.description || '无'}</p>
              </div>
              <div><span className="text-eleball-text-secondary">System Prompt：</span>
                <pre className="mt-1 p-3 bg-gray-50 rounded-xl text-xs whitespace-pre-wrap max-h-40 overflow-auto">{detailModal.system_prompt || '无'}</pre>
              </div>
              <div><span className="text-eleball-text-secondary">Tools JSON：</span>
                <pre className="mt-1 p-3 bg-gray-50 rounded-xl text-xs whitespace-pre-wrap max-h-40 overflow-auto">{detailModal.tools_json || '无'}</pre>
              </div>
              {(() => {
                const manifest = parseManifest(detailModal.manifest_json)
                if (!manifest) return null
                return (
                  <>
                    <div><span className="text-eleball-text-secondary">Tool Manifest：</span>
                      <div className="mt-1 p-3 bg-gray-50 rounded-xl space-y-2 text-xs">
                        <div><span className="text-eleball-text-secondary">驱动：</span>{manifest.driver || '-'}</div>
                        <div><span className="text-eleball-text-secondary">工具 ID：</span>{manifest.id || '-'}</div>
                        <div><span className="text-eleball-text-secondary">分类：</span>{manifest.category || '-'}</div>
                        <div><span className="text-eleball-text-secondary">权限：</span>{(manifest.permissions || []).join(', ') || '-'}</div>
                        <div><span className="text-eleball-text-secondary">操作：</span>{(manifest.actions || []).map(a => a.name).join(', ') || '-'}</div>
                      </div>
                    </div>
                    <div><span className="text-eleball-text-secondary">Manifest JSON：</span>
                      <pre className="mt-1 p-3 bg-gray-50 rounded-xl text-xs whitespace-pre-wrap max-h-40 overflow-auto">{detailModal.manifest_json}</pre>
                    </div>
                    <div><span className="text-eleball-text-secondary">依赖状态：</span>
                      {depLoading ? (
                        <p className="mt-1 text-xs text-eleball-text-secondary">加载中...</p>
                      ) : depStatus ? (
                        <div className="mt-1 p-3 bg-gray-50 rounded-xl space-y-2 text-xs">
                          <div className="flex items-center gap-2">
                            <span>驱动别名：</span>
                            <span className="font-mono">{depStatus.driver || '-'}</span>
                            {depStatus.driver_registered ? (
                              <span className="px-1.5 py-0.5 rounded text-[10px] bg-green-50 text-green-700 border border-green-100">已注册</span>
                            ) : (
                              <span className="px-1.5 py-0.5 rounded text-[10px] bg-red-50 text-red-700 border border-red-100">未注册</span>
                            )}
                          </div>
                          {depStatus.driver_name && <div><span className="text-eleball-text-secondary">驱动名称：</span>{depStatus.driver_name}</div>}
                          {depStatus.module_id && (
                            <div className="flex items-center gap-2">
                              <span>模块 ID：</span>
                              <span className="font-mono">{depStatus.module_id}</span>
                              {depStatus.module_registered ? (
                                <span className="px-1.5 py-0.5 rounded text-[10px] bg-green-50 text-green-700 border border-green-100">已注册</span>
                              ) : (
                                <span className="px-1.5 py-0.5 rounded text-[10px] bg-red-50 text-red-700 border border-red-100">未注册</span>
                              )}
                              {depStatus.module_online === true && <span className="px-1.5 py-0.5 rounded text-[10px] bg-green-50 text-green-700 border border-green-100">在线</span>}
                              {depStatus.module_online === false && <span className="px-1.5 py-0.5 rounded text-[10px] bg-gray-100 text-gray-600 border border-gray-200">离线</span>}
                            </div>
                          )}
                          {!depStatus.driver_registered && (
                            <p className="text-orange-600">驱动别名未注册：SKU 通过审批后不会出现在集市，也无法被 Agent 调用，直到开发者完成服务注册。</p>
                          )}
                          {depStatus.driver_registered && depStatus.module_id && !depStatus.module_registered && (
                            <p className="text-orange-600">对应模块未注册：开发者需使用 auth_token 完成模块自助注册后，该 SKU 才会上架。</p>
                          )}
                        </div>
                      ) : (
                        <p className="mt-1 text-xs text-eleball-text-secondary">无法获取</p>
                      )}
                    </div>
                  </>
                )
              })()}
            </div>
            <div className="p-6 border-t border-eleball-outline flex justify-end gap-3">
              <button onClick={() => { setDetailModal(null); setDepStatus(null) }} className="px-4 py-2 rounded-xl text-sm font-medium border border-eleball-outline hover:bg-gray-50">关闭</button>
              {detailModal.status === 'pending' && (
                <>
                  <button
                    onClick={() => { setDetailModal(null); handleApprove(detailModal.id) }}
                    className="px-4 py-2 rounded-xl text-sm font-medium bg-green-600 text-white hover:bg-green-700"
                  >
                    通过
                  </button>
                  <button onClick={() => { setDetailModal(null); handleReject(detailModal.id) }} className="px-4 py-2 rounded-xl text-sm font-medium bg-red-600 text-white hover:bg-red-700">拒绝</button>
                </>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

export default AgentsReview
