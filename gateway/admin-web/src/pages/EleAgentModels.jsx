import { useEffect, useState } from 'react'
import client from '../api/client'

/**
 * Ele Agent 模型配置管理页
 * 管理后台通过 /v1/admin/eleagent/models 维护 Ele Agent 可调用的子平台模型。
 */
export default function EleAgentModels() {
  const [items, setItems] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState(null)
  const [draggedId, setDraggedId] = useState(null)
  const [dragOverId, setDragOverId] = useState(null)
  const [savingOrder, setSavingOrder] = useState(false)

  const emptyForm = {
    provider: 'qwen',
    protocol: 'openai_compatible',
    model_name: '',
    display_name: '',
    base_url: 'https://api.siliconflow.cn/v1',
    api_key: '',
    priority: 0,
    input_price_per_call: 0,
    price_per_call: 0,
    price_per_generation: 0,
    video_min_duration: 0,
    video_max_duration: 0,
    video_duration_step: 1,
    supports_chat: true,
    supports_vision: false,
    supports_image: false,
    supports_video: false,
    supports_image_input: false,
    supports_continuous_context: false,
    supports_tools: false,
  }
  const [form, setForm] = useState(emptyForm)

  // 批量导出 / 导入
  const [showExport, setShowExport] = useState(false)
  const [exportIncludeKeys, setExportIncludeKeys] = useState(false)
  const [exporting, setExporting] = useState(false)
  const [showImport, setShowImport] = useState(false)
  const [importItems, setImportItems] = useState(null)
  const [importFileName, setImportFileName] = useState('')
  const [importError, setImportError] = useState('')
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState(null)

  const fetchItems = async () => {
    setLoading(true)
    setError('')
    try {
      const res = await client.get('/admin/eleagent/models')
      if (res && typeof res === 'object' && Array.isArray(res.items)) {
        setItems(res.items)
      } else {
        setError('加载失败')
      }
    } catch (err) {
      setError(err.response?.data?.message || err.message || err || '网络错误')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchItems()
  }, [])

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    // 能力校验：对话/图片/视频至少勾选一项，与后端 validateProtocolCapabilities 一致
    if (!form.supports_chat && !form.supports_image && !form.supports_video) {
      setError('对话生成、图片生成、视频生成至少需要勾选一项能力')
      return
    }
    try {
      if (editing) {
        const body = {
          provider: form.provider,
          protocol: form.protocol,
          model_name: form.model_name,
          display_name: form.display_name,
          base_url: form.base_url,
          is_enabled: form.is_enabled,
          supports_chat: form.supports_chat,
          supports_vision: form.supports_vision,
          supports_image: form.supports_image,
          supports_video: form.supports_video,
          supports_image_input: form.supports_image_input,
          supports_continuous_context: form.supports_continuous_context,
          supports_tools: form.supports_tools,
          priority: Math.round(Number(form.priority)) || 0,
          input_price_per_call: Math.round(Number(form.input_price_per_call)) || 0,
          price_per_call: Math.round(Number(form.price_per_call)) || 0,
          price_per_generation: Math.round(Number(form.price_per_generation)) || 0,
          video_min_duration: Math.round(Number(form.video_min_duration)) || 0,
          video_max_duration: Math.round(Number(form.video_max_duration)) || 0,
          video_duration_step: Math.round(Number(form.video_duration_step)) || 1,
        }
        await client.patch(`/admin/eleagent/models/${editing.id}`, body)
      } else {
        const body = {
          provider: form.provider,
          protocol: form.protocol,
          model_name: form.model_name,
          display_name: form.display_name,
          base_url: form.base_url,
          api_key: form.api_key,
          priority: Math.round(Number(form.priority)) || 0,
          input_price_per_call: Math.round(Number(form.input_price_per_call)) || 0,
          price_per_call: Math.round(Number(form.price_per_call)) || 0,
          price_per_generation: Math.round(Number(form.price_per_generation)) || 0,
          video_min_duration: Math.round(Number(form.video_min_duration)) || 0,
          video_max_duration: Math.round(Number(form.video_max_duration)) || 0,
          video_duration_step: Math.round(Number(form.video_duration_step)) || 1,
          supports_chat: form.supports_chat,
          supports_vision: form.supports_vision,
          supports_image: form.supports_image,
          supports_video: form.supports_video,
          supports_image_input: form.supports_image_input,
          supports_continuous_context: form.supports_continuous_context,
          supports_tools: form.supports_tools,
        }
        await client.post('/admin/eleagent/models', body)
      }
      setShowForm(false)
      setEditing(null)
      setForm(emptyForm)
      fetchItems()
    } catch (err) {
      setError(err.response?.data?.message || err.message || err || '提交失败')
    }
  }

  const handleEdit = (item) => {
    setEditing(item)
    setForm({
      provider: item.provider,
      protocol: item.protocol || 'openai_compatible',
      model_name: item.model_name,
      display_name: item.display_name || '',
      base_url: item.base_url || '',
      api_key: '',
      priority: item.priority || 0,
      input_price_per_call: item.input_price_per_call || 0,
      price_per_call: item.price_per_call || 0,
      price_per_generation: item.price_per_generation || 0,
      video_min_duration: item.video_min_duration || 0,
      video_max_duration: item.video_max_duration || 0,
      video_duration_step: item.video_duration_step || 1,
      is_enabled: item.is_enabled,
      supports_chat: item.supports_chat || false,
      supports_vision: item.supports_vision || false,
      supports_image: item.supports_image || false,
      supports_video: item.supports_video || false,
      supports_image_input: item.supports_image_input || false,
      supports_continuous_context: item.supports_continuous_context || false,
      supports_tools: item.supports_tools || false,
    })
    setShowForm(true)
  }

  const handleDelete = async (id) => {
    if (!window.confirm('确定删除该模型配置？')) return
    try {
      await client.delete(`/admin/eleagent/models/${id}`)
      fetchItems()
    } catch (err) {
      setError(err.response?.data?.message || err.message || err || '删除失败')
    }
  }

  const handleRotateKey = async (item) => {
    const key = window.prompt('请输入新的 API Key')
    if (!key) return
    try {
      await client.post(`/admin/eleagent/models/${item.id}/rotate-key`, { api_key: key })
      fetchItems()
    } catch (err) {
      setError(err.response?.data?.message || err.message || err || '轮换失败')
    }
  }

  const handleDragStart = (e, id) => {
    setDraggedId(id)
    e.dataTransfer.effectAllowed = 'move'
    e.dataTransfer.setData('text/plain', id)
  }

  const handleDragOver = (e, id) => {
    e.preventDefault()
    if (id !== draggedId) {
      setDragOverId(id)
    }
  }

  const persistOrder = async (orderedItems) => {
    setSavingOrder(true)
    setError('')
    try {
      await Promise.all(
        orderedItems.map((item, index) => {
          if (item.priority === index) return Promise.resolve()
          return client.patch(`/admin/eleagent/models/${item.id}`, { priority: index })
        })
      )
      fetchItems()
    } catch (err) {
      setError(err.response?.data?.message || err.message || err || '排序保存失败')
    } finally {
      setSavingOrder(false)
    }
  }

  const handleDrop = async (e, targetId) => {
    e.preventDefault()
    if (!draggedId || draggedId === targetId) {
      setDraggedId(null)
      setDragOverId(null)
      return
    }
    const draggedItem = items.find((i) => i.id === draggedId)
    const filtered = items.filter((i) => i.id !== draggedId)
    const targetIndex = filtered.findIndex((i) => i.id === targetId)
    const newItems = [
      ...filtered.slice(0, targetIndex),
      draggedItem,
      ...filtered.slice(targetIndex)
    ]
    setItems(newItems)
    setDraggedId(null)
    setDragOverId(null)
    await persistOrder(newItems)
  }

  const handleDragEnd = () => {
    setDraggedId(null)
    setDragOverId(null)
  }

  // ===== 批量导出 =====
  const handleExport = async () => {
    setExporting(true)
    setError('')
    try {
      const blob = await client.get('/admin/eleagent/models/export', {
        params: { include_keys: exportIncludeKeys },
        responseType: 'blob'
      })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `eleagent-models-${new Date().toISOString().slice(0, 10).replace(/-/g, '')}.json`
      a.click()
      URL.revokeObjectURL(url)
      setShowExport(false)
    } catch (err) {
      setError(err.message || err || '导出失败')
    } finally {
      setExporting(false)
    }
  }

  // ===== 批量导入 =====
  const openImportModal = () => {
    setImportItems(null)
    setImportFileName('')
    setImportError('')
    setImportResult(null)
    setShowImport(true)
  }

  // 解析导入文件：支持导出完整 JSON（含 items）或纯配置数组
  const handleImportFile = (e) => {
    const file = e.target.files?.[0]
    if (!file) return
    setImportFileName(file.name)
    setImportError('')
    setImportResult(null)
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const parsed = JSON.parse(reader.result)
        const items = Array.isArray(parsed) ? parsed : parsed?.items
        if (!Array.isArray(items) || items.length === 0) {
          throw new Error('未找到配置数组（items）')
        }
        setImportItems(items)
      } catch (err) {
        setImportItems(null)
        setImportError('文件解析失败：' + (err.message || err))
      }
    }
    reader.readAsText(file)
  }

  const handleImportConfirm = async () => {
    if (!importItems || importItems.length === 0) return
    setImporting(true)
    setImportError('')
    try {
      const res = await client.post('/admin/eleagent/models/import', { items: importItems })
      setImportResult(res)
      fetchItems()
    } catch (err) {
      setImportError(err.message || err || '导入失败')
    } finally {
      setImporting(false)
    }
  }

  // 判断导入行相对现有配置是新增还是更新（后端按 provider + model_name 匹配）
  const importRowKind = (row) =>
    items.some((i) => i.provider === row.provider && i.model_name === row.model_name) ? '更新' : '新增'

  // 下载导入模板（含字段说明与两条示例配置）
  const downloadTemplate = () => {
    const tpl = {
      version: 1,
      include_keys: true,
      usage: '本文件可直接用于批量导入：按 provider + model_name 匹配，已存在则只覆盖文件中出现的字段（api_key 省略=保留原 Key，提供=轮换），不存在则创建（需完整字段与 api_key）。usage 与 field_notes 仅供阅读，导入时忽略。',
      field_notes: {
        provider: '平台标识（自定义，用于配置匹配与统计），如 kimi / volcengine / agnes / qwen；与 model_name 组成唯一匹配键',
        protocol: '上游协议：openai_compatible（对话）/ anthropic_messages（对话）/ agnes_image（图片）/ agnes_video（视频）/ seedance（火山视频）/ seedream（火山方舟·即梦图片）/ openai_image、openai_video（预留）；缺省为 openai_compatible',
        model_name: '上游模型 ID，如 k3、doubao-seedream-4-0-250828、doubao-seedance-1-0-pro-250528',
        display_name: '展示名称（可选），客户端模型列表中显示',
        base_url: '上游 API 地址；新建必填，更新时省略表示保持原值',
        api_key: '明文 API Key；新建必填；更新时省略=保留原 Key，提供=轮换 Key',
        is_enabled: '是否启用；更新时省略表示保持原启用状态',
        supports_chat: '能力开关：支持文字对话（对话页）；纯图片/纯视频生成模型应为 false，对话/图片/视频至少需开启一项',
        supports_vision: '能力开关：支持视觉理解（图片输入）',
        supports_image: '能力开关：支持图片生成（需搭配 agnes_image / seedream 协议）',
        supports_video: '能力开关：支持视频生成（需搭配 agnes_video / seedance 协议）',
        supports_image_input: '能力开关：支持上传图片作为生成输入（图生图/图生视频）',
        supports_continuous_context: '能力开关（产品声明）：支持连续上下文创作，运行时由 protocol 决定',
        supports_tools: '能力开关：支持 Agent 工具调用（Function Call）',
        priority: '优先级（整数 ≥0，越小越靠前），用于客户端模型列表排序',
        input_price_per_call: '输入单价（弹丸 / 1M tokens，≥0），0 表示免费',
        price_per_call: '输出单价（弹丸 / 1M tokens，≥0），0 表示免费',
        price_per_generation: '按次附加费（弹丸/次，≥0），与输入/输出 token 费用相加，适用于对话/图片/视频模型，0 表示不附加',
        video_min_duration: '视频最小时长（秒，≥0），0 表示不限制；不能超过 video_max_duration',
        video_max_duration: '视频最大时长（秒，≥0），0 表示不限制；示例：Seedance 1.0 Pro 支持 5~10 秒',
        video_duration_step: '视频时长步长（秒，≥1），前端按 min~max 以步长生成可选档位；示例：5~10 秒步长 5 → 可选 5s / 10s'
      },
      items: [
        {
          provider: 'kimi',
          protocol: 'openai_compatible',
          model_name: 'k3',
          display_name: 'Kimi K3',
          base_url: 'https://api.kimi.com/coding/v1',
          api_key: 'sk-在此填入APIKey',
          is_enabled: true,
          supports_chat: true,
          supports_vision: true,
          supports_image: false,
          supports_video: false,
          supports_image_input: false,
          supports_continuous_context: false,
          supports_tools: true,
          priority: 0,
          input_price_per_call: 0,
          price_per_call: 0,
          price_per_generation: 0,
          video_min_duration: 0,
          video_max_duration: 0,
          video_duration_step: 1
        },
        {
          provider: 'volcengine',
          protocol: 'seedance',
          model_name: 'doubao-seedance-1-0-pro-250528',
          display_name: 'Seedance 1.0 Pro',
          base_url: 'https://ark.cn-beijing.volces.com/api/v3',
          api_key: 'ark-在此填入APIKey',
          is_enabled: true,
          supports_chat: false,
          supports_vision: false,
          supports_image: false,
          supports_video: true,
          supports_image_input: true,
          supports_continuous_context: false,
          supports_tools: false,
          priority: 0,
          input_price_per_call: 0,
          price_per_call: 0,
          price_per_generation: 0,
          video_min_duration: 5,
          video_max_duration: 10,
          video_duration_step: 5
        }
      ]
    }
    const blob = new Blob([JSON.stringify(tpl, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'eleagent-models-template.json'
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-xl font-bold text-eleball-text">Ele Agent 模型配置</h2>
          <p className="text-sm text-eleball-text-secondary mt-1">
            配置 Ele Agent 后端实际调用的子平台模型（对话模型如 qwen、deepseek；视觉模型如 agnes、seedance）。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={openImportModal}
            className="px-4 py-2 bg-white border border-eleball-outline text-eleball-text rounded-xl text-sm font-medium hover:bg-gray-50 transition-colors"
          >
            批量导入
          </button>
          <button
            onClick={() => {
              setExportIncludeKeys(false)
              setShowExport(true)
            }}
            className="px-4 py-2 bg-white border border-eleball-outline text-eleball-text rounded-xl text-sm font-medium hover:bg-gray-50 transition-colors"
          >
            批量导出
          </button>
          <button
            onClick={() => {
              setEditing(null)
              setForm(emptyForm)
              setShowForm(true)
            }}
            className="px-4 py-2 bg-eleball-primary text-white rounded-xl text-sm font-medium hover:bg-eleball-primary-dark transition-colors"
          >
            + 新增配置
          </button>
        </div>
      </div>

      {/* 导出确认弹窗 */}
      {showExport && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30">
          <div className="bg-white rounded-2xl p-6 w-[420px] shadow-xl">
            <h3 className="text-lg font-semibold mb-2">导出模型配置</h3>
            <p className="text-sm text-eleball-text-secondary mb-4">
              将全部模型配置导出为 JSON 文件，可用于备份或迁移到其他环境；导出的文件可直接用于「批量导入」。
            </p>
            <label className="flex items-start gap-3 mb-4 cursor-pointer">
              <input
                type="checkbox"
                checked={exportIncludeKeys}
                onChange={(e) => setExportIncludeKeys(e.target.checked)}
                className="w-4 h-4 mt-0.5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <span className="text-sm">
                包含 API Key 明文
                <span className="block text-xs text-red-500 mt-0.5">
                  勾选后导出文件包含解密后的密钥明文，请妥善保管，勿外发。
                </span>
              </span>
            </label>
            <div className="flex justify-end gap-3">
              <button
                onClick={() => setShowExport(false)}
                className="px-4 py-2 rounded-xl text-sm font-medium text-eleball-text-secondary hover:bg-gray-50 transition-colors"
              >
                取消
              </button>
              <button
                onClick={handleExport}
                disabled={exporting}
                className="px-4 py-2 bg-eleball-primary text-white rounded-xl text-sm font-medium hover:bg-eleball-primary-dark transition-colors disabled:opacity-50"
              >
                {exporting ? '导出中…' : '导出 JSON'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 导入弹窗：选择文件 → 表格预览 → 确认导入 → 结果反馈 */}
      {showImport && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30">
          <div className="bg-white rounded-2xl p-6 w-[720px] max-w-[95vw] max-h-[85vh] overflow-y-auto shadow-xl">
            <h3 className="text-lg font-semibold mb-2">批量导入模型配置</h3>
            <p className="text-sm text-eleball-text-secondary mb-4">
              按「平台标识 + 模型名」匹配：已存在则更新——只覆盖文件中出现的字段，未写到的字段保持原值（不含 api_key 时保留原 Key）；不存在则新建（必须含 api_key）。
              <button onClick={downloadTemplate} className="ml-1 text-eleball-primary hover:underline">
                下载模板
              </button>
            </p>

            <div className="mb-4">
              <input
                type="file"
                accept="application/json,.json"
                onChange={handleImportFile}
                className="block w-full text-sm text-eleball-text-secondary file:mr-3 file:px-4 file:py-2 file:rounded-xl file:border file:border-eleball-outline file:bg-white file:text-sm file:font-medium file:text-eleball-text hover:file:bg-gray-50"
              />
              {importFileName && (
                <p className="text-xs text-eleball-text-secondary mt-1.5">已选择：{importFileName}</p>
              )}
            </div>

            {importError && (
              <div className="mb-4 p-3 rounded-xl bg-red-50 text-red-600 text-sm">{importError}</div>
            )}

            {importItems && !importResult && (
              <>
                <p className="text-sm mb-2">
                  解析到 <b>{importItems.length}</b> 条配置：
                  <span className="text-green-600 ml-2">
                    新增 {importItems.filter((r) => importRowKind(r) === '新增').length}
                  </span>
                  <span className="text-blue-600 ml-2">
                    更新 {importItems.filter((r) => importRowKind(r) === '更新').length}
                  </span>
                </p>
                <div className="border border-eleball-outline rounded-xl overflow-hidden mb-4 max-h-64 overflow-y-auto">
                  <table className="w-full text-sm text-left">
                    <thead className="bg-gray-50 text-eleball-text-secondary sticky top-0">
                      <tr>
                        <th className="px-3 py-2 font-medium">#</th>
                        <th className="px-3 py-2 font-medium">平台 / 模型</th>
                        <th className="px-3 py-2 font-medium">协议</th>
                        <th className="px-3 py-2 font-medium">含 Key</th>
                        <th className="px-3 py-2 font-medium">操作</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-eleball-outline">
                      {importItems.slice(0, 100).map((row, idx) => (
                        <tr key={idx}>
                          <td className="px-3 py-2 text-eleball-text-secondary">{idx + 1}</td>
                          <td className="px-3 py-2">{row.provider} / {row.model_name}</td>
                          <td className="px-3 py-2 text-eleball-text-secondary">{row.protocol || 'openai_compatible'}</td>
                          <td className="px-3 py-2">{row.api_key ? '是' : '否'}</td>
                          <td className="px-3 py-2">
                            <span className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                              importRowKind(row) === '新增' ? 'bg-green-50 text-green-600' : 'bg-blue-50 text-blue-600'
                            }`}>
                              {importRowKind(row)}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                  {importItems.length > 100 && (
                    <p className="px-3 py-2 text-xs text-eleball-text-secondary bg-gray-50">
                      仅预览前 100 条，共 {importItems.length} 条
                    </p>
                  )}
                </div>
              </>
            )}

            {importResult && (
              <div className="mb-4">
                <p className="text-sm mb-2">
                  导入完成：
                  <span className="text-green-600 ml-2">新增 {importResult.created}</span>
                  <span className="text-blue-600 ml-2">更新 {importResult.updated}</span>
                  <span className={`ml-2 ${importResult.failed?.length ? 'text-red-600' : 'text-eleball-text-secondary'}`}>
                    失败 {importResult.failed?.length || 0}
                  </span>
                </p>
                {importResult.failed?.length > 0 && (
                  <div className="border border-red-200 rounded-xl overflow-hidden max-h-48 overflow-y-auto">
                    <table className="w-full text-sm text-left">
                      <thead className="bg-red-50 text-red-600 sticky top-0">
                        <tr>
                          <th className="px-3 py-2 font-medium">#</th>
                          <th className="px-3 py-2 font-medium">平台 / 模型</th>
                          <th className="px-3 py-2 font-medium">失败原因</th>
                        </tr>
                      </thead>
                      <tbody className="divide-y divide-red-100">
                        {importResult.failed.map((f, idx) => (
                          <tr key={idx}>
                            <td className="px-3 py-2 text-eleball-text-secondary">{f.index + 1}</td>
                            <td className="px-3 py-2">{f.provider} / {f.model_name}</td>
                            <td className="px-3 py-2 text-red-600">{f.error}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                )}
              </div>
            )}

            <div className="flex justify-end gap-3">
              <button
                onClick={() => setShowImport(false)}
                className="px-4 py-2 rounded-xl text-sm font-medium text-eleball-text-secondary hover:bg-gray-50 transition-colors"
              >
                关闭
              </button>
              {importItems && !importResult && (
                <button
                  onClick={handleImportConfirm}
                  disabled={importing}
                  className="px-4 py-2 bg-eleball-primary text-white rounded-xl text-sm font-medium hover:bg-eleball-primary-dark transition-colors disabled:opacity-50"
                >
                  {importing ? '导入中…' : `确认导入 ${importItems.length} 条`}
                </button>
              )}
            </div>
          </div>
        </div>
      )}

      {error && (
        <div className="p-3 rounded-xl bg-red-50 text-red-600 text-sm">{error}</div>
      )}

      {showForm && (
        <div className="bg-white rounded-2xl p-6 border border-eleball-outline shadow-sm">
          <h3 className="text-lg font-semibold mb-4">
            {editing ? '编辑配置' : '新增配置'}
          </h3>
          <form onSubmit={handleSubmit} className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium mb-1.5">平台标识</label>
              <input
                type="text"
                value={form.provider}
                onChange={(e) => setForm({ ...form, provider: e.target.value })}
                placeholder="如 qwen、deepseek、openai、anthropic、agnes、seedance 或自定义标识"
                required
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
              <p className="text-xs text-eleball-text-secondary mt-1">
                仅用于配置匹配与统计，OpenAI 兼容平台可任意填写。
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">上游协议</label>
              <select
                value={form.protocol}
                onChange={(e) => {
                  const protocol = e.target.value
                  let baseUrl = form.base_url
                  // 切换协议时联动默认 BaseURL，仅当用户未手动修改或仍为默认值时
                  const defaultUrls = [
                    '',
                    'https://api.siliconflow.cn/v1',
                    'https://api.openai.com/v1',
                    'https://api.anthropic.com/v1',
                    'https://apihub.agnes-ai.com/v1/images/generations',
                    'https://apihub.agnes-ai.com/v1/videos',
                    'https://ark.cn-beijing.volces.com/api/v3'
                  ]
                  if (protocol === 'anthropic_messages' && defaultUrls.includes(baseUrl)) {
                    baseUrl = 'https://api.anthropic.com/v1'
                  } else if (protocol === 'openai_compatible' && defaultUrls.includes(baseUrl)) {
                    baseUrl = 'https://api.siliconflow.cn/v1'
                  } else if (protocol === 'agnes_image' && defaultUrls.includes(baseUrl)) {
                    baseUrl = 'https://apihub.agnes-ai.com/v1/images/generations'
                  } else if (protocol === 'agnes_video' && defaultUrls.includes(baseUrl)) {
                    baseUrl = 'https://apihub.agnes-ai.com/v1/videos'
                  } else if (protocol === 'seedance' && defaultUrls.includes(baseUrl)) {
                    baseUrl = 'https://ark.cn-beijing.volces.com/api/v3'
                  } else if (protocol === 'seedream' && defaultUrls.includes(baseUrl)) {
                    baseUrl = 'https://ark.cn-beijing.volces.com/api/v3'
                  }
                  setForm({ ...form, protocol, base_url: baseUrl })
                }}
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20 bg-white"
              >
                <option value="openai_compatible">OpenAI 兼容协议</option>
                <option value="anthropic_messages">Anthropic Messages API</option>
                <option value="agnes_image">Agnes Image（文生图/图生图）</option>
                <option value="agnes_video">Agnes Video（文生视频/图生视频）</option>
                <option value="seedance">Seedance（火山引擎视频生成）</option>
                <option value="seedream">Seedream（火山方舟/即梦图片生成）</option>
              </select>
              <p className="text-xs text-eleball-text-secondary mt-1">
                决定网关如何转换请求到上游厂商格式。视觉生成模型请选择对应视觉协议。
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">模型名</label>
              <input
                type="text"
                value={form.model_name}
                onChange={(e) => setForm({ ...form, model_name: e.target.value })}
                placeholder="如 Qwen/Qwen3-8B（不含平台前缀）"
                required
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
              <p className="text-xs text-eleball-text-secondary mt-1">
                App 中看到的完整模型名为「平台标识/模型名」，这里只填斜杠后面的部分。
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">展示名称</label>
              <input
                type="text"
                value={form.display_name}
                onChange={(e) => setForm({ ...form, display_name: e.target.value })}
                placeholder="如 通义千问 Qwen3-8B"
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">Base URL</label>
              <input
                type="text"
                value={form.base_url}
                onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                placeholder="https://api.xxx.com/v1"
                required
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
            </div>
            {!editing && (
              <div>
                <label className="block text-sm font-medium mb-1.5">API Key</label>
                <input
                  type="password"
                  value={form.api_key}
                  onChange={(e) => setForm({ ...form, api_key: e.target.value })}
                  placeholder="sk-..."
                  required
                  className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
                />
              </div>
            )}
            <div>
              <label className="block text-sm font-medium mb-1.5">优先级</label>
              <input
                type="number"
                min="0"
                step="1"
                value={form.priority}
                onChange={(e) => setForm({ ...form, priority: e.target.value })}
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">输入单价（弹丸 / M tokens）</label>
              <input
                type="number"
                min="0"
                step="1"
                value={form.input_price_per_call}
                onChange={(e) => setForm({ ...form, input_price_per_call: e.target.value })}
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
              <p className="text-xs text-eleball-text-secondary mt-1">
                0 表示免费；按输入 token 用量计费。
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">输出单价（弹丸 / M tokens）</label>
              <input
                type="number"
                min="0"
                step="1"
                value={form.price_per_call}
                onChange={(e) => setForm({ ...form, price_per_call: e.target.value })}
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
              <p className="text-xs text-eleball-text-secondary mt-1">
                0 表示免费；按输出 token 用量计费。
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">按次附加费（弹丸 / 次）</label>
              <input
                type="number"
                min="0"
                step="1"
                value={form.price_per_generation}
                onChange={(e) => setForm({ ...form, price_per_generation: e.target.value })}
                className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
              />
              <p className="text-xs text-eleball-text-secondary mt-1">
                0 表示不附加；大于 0 时与输入/输出 token 费用相加，适用于对话/图片/视频模型。
              </p>
            </div>
            {form.supports_video && (
              <>
                <div>
                  <label className="block text-sm font-medium mb-1.5">视频最小时长（秒）</label>
                  <input
                    type="number"
                    min="0"
                    step="1"
                    value={form.video_min_duration}
                    onChange={(e) => setForm({ ...form, video_min_duration: e.target.value })}
                    className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">视频最大时长（秒）</label>
                  <input
                    type="number"
                    min="0"
                    step="1"
                    value={form.video_max_duration}
                    onChange={(e) => setForm({ ...form, video_max_duration: e.target.value })}
                    className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
                  />
                  <p className="text-xs text-eleball-text-secondary mt-1">
                    0 表示不限制，前端自由输入；配置后前端按范围生成可选时长。
                  </p>
                </div>
                <div>
                  <label className="block text-sm font-medium mb-1.5">视频时长步长（秒）</label>
                  <input
                    type="number"
                    min="1"
                    step="1"
                    value={form.video_duration_step}
                    onChange={(e) => setForm({ ...form, video_duration_step: e.target.value })}
                    className="w-full px-3 py-2 rounded-xl border border-eleball-outline focus:outline-none focus:ring-2 focus:ring-eleball-primary/20"
                  />
                </div>
              </>
            )}
            <div className="flex items-center gap-3">
              <input
                id="supports_chat"
                type="checkbox"
                checked={!!form.supports_chat}
                onChange={(e) => setForm({ ...form, supports_chat: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_chat" className="text-sm font-medium">
                支持对话生成（文字对话）
                <span className="block text-xs font-normal text-eleball-text-secondary">
                  勾选后出现在对话页模型列表；纯图片/纯视频生成模型请勿勾选
                </span>
              </label>
            </div>
            <div className="flex items-center gap-3">
              <input
                id="supports_vision"
                type="checkbox"
                checked={!!form.supports_vision}
                onChange={(e) => setForm({ ...form, supports_vision: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_vision" className="text-sm font-medium">
                支持视觉（图片理解）
                <span className="block text-xs font-normal text-eleball-text-secondary">
                  指对话模型可理解上传的图片等多媒体内容，非图片生成
                </span>
              </label>
            </div>
            <div className="flex items-center gap-3">
              <input
                id="supports_image"
                type="checkbox"
                checked={!!form.supports_image}
                onChange={(e) => setForm({ ...form, supports_image: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_image" className="text-sm font-medium">
                支持图片生成
              </label>
            </div>
            <div className="flex items-center gap-3">
              <input
                id="supports_video"
                type="checkbox"
                checked={!!form.supports_video}
                onChange={(e) => setForm({ ...form, supports_video: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_video" className="text-sm font-medium">
                支持视频生成
              </label>
            </div>
            <div className="flex items-center gap-3">
              <input
                id="supports_image_input"
                type="checkbox"
                checked={!!form.supports_image_input}
                onChange={(e) => setForm({ ...form, supports_image_input: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_image_input" className="text-sm font-medium">
                支持上传图片作为输入（图生图/图生视频）
              </label>
            </div>
            <div className="flex items-center gap-3">
              <input
                id="supports_continuous_context"
                type="checkbox"
                checked={!!form.supports_continuous_context}
                onChange={(e) => setForm({ ...form, supports_continuous_context: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_continuous_context" className="text-sm font-medium">
                支持连续上下文记忆（基于历史生成继续创作）
              </label>
            </div>
            <div className="flex items-center gap-3">
              <input
                id="supports_tools"
                type="checkbox"
                checked={!!form.supports_tools}
                onChange={(e) => setForm({ ...form, supports_tools: e.target.checked })}
                className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
              />
              <label htmlFor="supports_tools" className="text-sm font-medium">
                支持 Agent 工具（Function Call）
              </label>
            </div>
            {editing && (
              <div className="flex items-center gap-3">
                <input
                  id="is_enabled"
                  type="checkbox"
                  checked={!!form.is_enabled}
                  onChange={(e) => setForm({ ...form, is_enabled: e.target.checked })}
                  className="w-4 h-4 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                />
                <label htmlFor="is_enabled" className="text-sm font-medium">
                  启用
                </label>
              </div>
            )}
            <div className="md:col-span-2 flex justify-end gap-3">
              <button
                type="button"
                onClick={() => setShowForm(false)}
                className="px-4 py-2 rounded-xl text-sm font-medium text-eleball-text-secondary hover:bg-gray-50 transition-colors"
              >
                取消
              </button>
              <button
                type="submit"
                className="px-4 py-2 bg-eleball-primary text-white rounded-xl text-sm font-medium hover:bg-eleball-primary-dark transition-colors"
              >
                保存
              </button>
            </div>
          </form>
        </div>
      )}

      <div className="bg-white rounded-2xl border border-eleball-outline overflow-hidden shadow-sm">
        <table className="w-full text-sm text-left">
          <thead className="bg-gray-50 text-eleball-text-secondary">
            <tr>
              <th className="px-4 py-3 font-medium w-10"></th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">平台 / 模型</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">协议</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">Base URL</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">状态</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">对话</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">视觉理解</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">图片生成</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">视频生成</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">上传输入</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">连续上下文</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">工具</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">优先级</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">输入单价</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">输出单价</th>
              <th className="px-6 py-3 font-medium whitespace-nowrap align-middle">次数单价</th>
              <th className="px-6 py-3 font-medium text-right whitespace-nowrap align-middle">操作</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-eleball-outline">
            {loading && items.length === 0 && (
              <tr>
                <td colSpan={15} className="px-6 py-8 text-center text-eleball-text-secondary">
                  加载中...
                </td>
              </tr>
            )}
            {!loading && items.length === 0 && (
              <tr>
                <td colSpan={15} className="px-6 py-8 text-center text-eleball-text-secondary">
                  暂无配置，点击右上角新增。
                </td>
              </tr>
            )}
            {items.map((item) => (
              <tr
                key={item.id}
                draggable
                onDragStart={(e) => handleDragStart(e, item.id)}
                onDragOver={(e) => handleDragOver(e, item.id)}
                onDrop={(e) => handleDrop(e, item.id)}
                onDragEnd={handleDragEnd}
                className={`hover:bg-gray-50 transition-colors ${
                  draggedId === item.id ? 'opacity-50' : ''
                } ${dragOverId === item.id ? 'bg-eleball-primary-light/40' : ''}`}
              >
                <td className="px-4 py-4 align-middle">
                  <span className="cursor-grab text-eleball-text-secondary select-none" title="拖动排序">
                    ⋮⋮
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <div className="font-medium text-eleball-text">
                    {item.display_name || item.model_name}
                  </div>
                  <div className="text-xs text-eleball-text-secondary mt-0.5">
                    {item.provider} / {item.model_name}
                  </div>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span className="inline-flex px-2 py-0.5 rounded-lg text-xs font-medium bg-blue-50 text-blue-600">
                    {item.protocol === 'anthropic_messages'
                      ? 'Anthropic'
                      : item.protocol === 'agnes_image'
                      ? 'Agnes Image'
                      : item.protocol === 'agnes_video'
                      ? 'Agnes Video'
                      : item.protocol === 'seedance'
                      ? 'Seedance'
                      : item.protocol === 'seedream'
                      ? 'Seedream'
                      : 'OpenAI 兼容'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle text-eleball-text-secondary max-w-xs truncate">
                  {item.base_url}
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.is_enabled
                        ? 'bg-green-50 text-green-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.is_enabled ? '启用' : '禁用'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_chat
                        ? 'bg-sky-50 text-sky-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_chat ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_vision
                        ? 'bg-purple-50 text-purple-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_vision ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_image
                        ? 'bg-indigo-50 text-indigo-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_image ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_video
                        ? 'bg-purple-50 text-purple-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_video ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_image_input
                        ? 'bg-purple-50 text-purple-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_image_input ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_continuous_context
                        ? 'bg-purple-50 text-purple-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_continuous_context ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">
                  <span
                    className={`inline-flex px-2 py-0.5 rounded-lg text-xs font-medium ${
                      item.supports_tools
                        ? 'bg-purple-50 text-purple-600'
                        : 'bg-gray-100 text-gray-500'
                    }`}
                  >
                    {item.supports_tools ? '是' : '否'}
                  </span>
                </td>
                <td className="px-6 py-4 align-middle">{item.priority}</td>
                <td className="px-6 py-4 align-middle">{item.input_price_per_call || 0}</td>
                <td className="px-6 py-4 align-middle">{item.price_per_call || 0}</td>
                <td className="px-6 py-4 align-middle">{item.price_per_generation || 0}</td>
                <td className="px-6 py-4 align-middle text-right space-x-2">
                  <button
                    onClick={() => handleRotateKey(item)}
                    className="text-xs px-2 py-1 rounded-lg bg-eleball-primary/10 text-eleball-primary-dark hover:bg-eleball-primary/20 transition-colors"
                  >
                    换 Key
                  </button>
                  <button
                    onClick={() => handleEdit(item)}
                    className="text-xs px-2 py-1 rounded-lg bg-gray-100 text-eleball-text hover:bg-gray-200 transition-colors"
                  >
                    编辑
                  </button>
                  <button
                    onClick={() => handleDelete(item.id)}
                    className="text-xs px-2 py-1 rounded-lg bg-red-50 text-red-600 hover:bg-red-100 transition-colors"
                  >
                    删除
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
