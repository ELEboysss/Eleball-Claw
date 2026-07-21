import { useEffect, useState, useMemo } from 'react'
import { rechargePackageApi } from '../api/client'

function fenToYuan(fen) {
  return ((fen || 0) / 100).toFixed(2)
}

function yuanToFen(yuan) {
  return Math.round(parseFloat(yuan || '0') * 100)
}

export default function RechargePackages() {
  const [items, setItems] = useState([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState(null)

  const emptyForm = {
    name: '',
    danwan: '',
    price_yuan: '',
    sort_order: 0,
    is_enabled: true,
    is_custom_multiplier: false,
    base_package_id: '',
    description: ''
  }
  const [form, setForm] = useState(emptyForm)

  const fetchItems = async () => {
    setLoading(true)
    setError('')
    try {
      const res = await rechargePackageApi.list()
      setItems(res?.items || [])
    } catch (err) {
      setError(err?.message || err || '加载失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchItems()
  }, [])

  // 可作为自定义数量套餐基础套餐的选项：非自定义、且不是当前编辑项
  const basePackageOptions = useMemo(() => {
    return items.filter((p) => !p.is_custom_multiplier && (!editing || p.id !== editing.id))
  }, [items, editing])

  const handleChange = (key, value) => {
    setForm((prev) => ({ ...prev, [key]: value }))
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    try {
      const body = {
        name: form.name,
        danwan: Number(form.danwan) || 0,
        price_yuan: Number(form.price_yuan) || 0,
        sort_order: Number(form.sort_order) || 0,
        is_enabled: Boolean(form.is_enabled),
        is_custom_multiplier: Boolean(form.is_custom_multiplier),
        base_package_id: form.base_package_id || null,
        description: form.description
      }
      if (editing) {
        await rechargePackageApi.update(editing.id, body)
      } else {
        await rechargePackageApi.create(body)
      }
      setShowForm(false)
      setEditing(null)
      setForm(emptyForm)
      fetchItems()
    } catch (err) {
      setError(err?.message || err || '提交失败')
    }
  }

  const handleEdit = (item) => {
    setEditing(item)
    setForm({
      name: item.name || '',
      danwan: item.danwan || '',
      price_yuan: fenToYuan(item.price_fen),
      sort_order: item.sort_order || 0,
      is_enabled: item.is_enabled,
      is_custom_multiplier: item.is_custom_multiplier,
      base_package_id: item.base_package_id || '',
      description: item.description || ''
    })
    setShowForm(true)
  }

  const handleDelete = async (id) => {
    if (!window.confirm('确定删除该充值套餐？')) return
    try {
      await rechargePackageApi.delete(id)
      fetchItems()
    } catch (err) {
      setError(err?.message || err || '删除失败')
    }
  }

  const handleAdd = () => {
    setEditing(null)
    setForm(emptyForm)
    setShowForm(true)
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">充值套餐</h1>
          <p className="text-eleball-text-secondary mt-1">管理用户端 /recharge 页面展示的充值套餐与价格</p>
        </div>
        <button onClick={handleAdd} className="btn-primary">
          新增套餐
        </button>
      </div>

      {error && (
        <div className="text-sm text-eleball-error bg-red-50 rounded-xl px-4 py-3">{error}</div>
      )}

      {showForm && (
        <div className="card space-y-4">
          <h3 className="text-base font-semibold">{editing ? '编辑套餐' : '新增套餐'}</h3>
          <form onSubmit={handleSubmit} className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium mb-1.5">套餐名称</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => handleChange('name', e.target.value)}
                placeholder="如：小杯、超大杯"
                className="input"
                required
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">价格（元）</label>
              <input
                type="number"
                step="0.01"
                min="0"
                value={form.price_yuan}
                onChange={(e) => handleChange('price_yuan', e.target.value)}
                className="input"
                required={!form.is_custom_multiplier}
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">到账弹丸数</label>
              <input
                type="number"
                min="0"
                value={form.danwan}
                onChange={(e) => handleChange('danwan', e.target.value)}
                className="input"
                required={!form.is_custom_multiplier}
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">排序</label>
              <input
                type="number"
                value={form.sort_order}
                onChange={(e) => handleChange('sort_order', e.target.value)}
                className="input"
              />
            </div>
            <div className="md:col-span-2">
              <label className="block text-sm font-medium mb-1.5">描述</label>
              <input
                type="text"
                value={form.description}
                onChange={(e) => handleChange('description', e.target.value)}
                className="input"
              />
            </div>
            <div className="flex items-center gap-4 md:col-span-2">
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.is_enabled}
                  onChange={(e) => handleChange('is_enabled', e.target.checked)}
                  className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                />
                <span className="text-sm">上架</span>
              </label>
              <label className="flex items-center gap-2 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.is_custom_multiplier}
                  onChange={(e) => {
                    handleChange('is_custom_multiplier', e.target.checked)
                    if (!e.target.checked) {
                      handleChange('base_package_id', '')
                    }
                  }}
                  className="w-5 h-5 rounded border-eleball-outline text-eleball-primary focus:ring-eleball-primary"
                />
                <span className="text-sm">自定义数量（如：重度依赖）</span>
              </label>
            </div>
            {form.is_custom_multiplier && (
              <div className="md:col-span-2">
                <label className="block text-sm font-medium mb-1.5">关联基础套餐</label>
                <select
                  value={form.base_package_id}
                  onChange={(e) => handleChange('base_package_id', e.target.value)}
                  className="input"
                  required={form.is_custom_multiplier}
                >
                  <option value="">请选择</option>
                  {basePackageOptions.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}（{p.danwan} 弹丸 / ¥{fenToYuan(p.price_fen)}）
                    </option>
                  ))}
                </select>
                <p className="text-xs text-eleball-text-secondary mt-1">
                  前端将以“基础套餐名 × 数量”展示，总价按基础套餐价格 × 数量计算。
                </p>
              </div>
            )}
            <div className="flex gap-3 md:col-span-2">
              <button type="submit" className="btn-primary">
                {editing ? '保存' : '创建'}
              </button>
              <button
                type="button"
                onClick={() => {
                  setShowForm(false)
                  setEditing(null)
                  setForm(emptyForm)
                }}
                className="btn-secondary"
              >
                取消
              </button>
            </div>
          </form>
        </div>
      )}

      <div className="card overflow-hidden">
        {loading ? (
          <div className="p-8 text-sm text-eleball-text-secondary">加载中...</div>
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-gray-50 text-eleball-text-secondary">
              <tr>
                <th className="text-left px-4 py-3 font-medium">名称</th>
                <th className="text-left px-4 py-3 font-medium">弹丸数</th>
                <th className="text-left px-4 py-3 font-medium">价格</th>
                <th className="text-left px-4 py-3 font-medium">排序</th>
                <th className="text-left px-4 py-3 font-medium">状态</th>
                <th className="text-left px-4 py-3 font-medium">类型</th>
                <th className="text-right px-4 py-3 font-medium">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-eleball-outline">
              {items.map((item) => (
                <tr key={item.id} className="hover:bg-gray-50">
                  <td className="px-4 py-3">
                    <div className="font-medium text-eleball-text">{item.name}</div>
                    {item.description && (
                      <div className="text-xs text-eleball-text-secondary">{item.description}</div>
                    )}
                  </td>
                  <td className="px-4 py-3">
                    {item.is_custom_multiplier
                      ? '按基础套餐 × 数量'
                      : (item.danwan || 0).toLocaleString('zh-CN')}
                  </td>
                  <td className="px-4 py-3">
                    {item.is_custom_multiplier ? '按基础套餐 × 数量' : `¥${fenToYuan(item.price_fen)}`}
                  </td>
                  <td className="px-4 py-3">{item.sort_order}</td>
                  <td className="px-4 py-3">
                    <span
                      className={`inline-flex px-2 py-1 rounded-lg text-xs font-medium ${
                        item.is_enabled
                          ? 'bg-emerald-50 text-emerald-700'
                          : 'bg-gray-100 text-gray-600'
                      }`}
                    >
                      {item.is_enabled ? '上架' : '下架'}
                    </span>
                  </td>
                  <td className="px-4 py-3 text-eleball-text-secondary">
                    {item.is_custom_multiplier ? `自定义 × ${item.base_package_id ? '已关联' : '未关联'}` : '固定套餐'}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button onClick={() => handleEdit(item)} className="text-eleball-primary hover:underline mr-3">
                      编辑
                    </button>
                    <button onClick={() => handleDelete(item.id)} className="text-eleball-error hover:underline">
                      删除
                    </button>
                  </td>
                </tr>
              ))}
              {items.length === 0 && (
                <tr>
                  <td colSpan={7} className="px-4 py-8 text-center text-eleball-text-secondary">
                    暂无充值套餐，点击右上角“新增套餐”创建。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
