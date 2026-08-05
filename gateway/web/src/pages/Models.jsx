import { useState, useEffect, useMemo } from 'react'
import useSEO from '../hooks/useSEO'
import { Cpu, Search, AlertCircle } from 'lucide-react'
import { modelApi } from '../api/client'
import PageHero from '../components/PageHero'

export default function Models() {
  useSEO('大模型中心', '支持 OpenAI/Claude/Gemini/DeepSeek 等，自带 Key 或 Ele Agent 代调用。')
  const [models, setModels] = useState([])
  const [cloudPrices, setCloudPrices] = useState({}) // model_name -> 云端价格（Ele Agent 代理模型用）
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selectedProvider, setSelectedProvider] = useState('全部')
  const [keyword, setKeyword] = useState('')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    modelApi
      .list()
      .then((data) => {
        if (cancelled) return
        setModels(Array.isArray(data) ? data : [])
      })
      .catch((err) => {
        if (cancelled) return
        setError(err.message || '获取模型列表失败')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    // 云端价格仅用于 Ele Agent 代理模型展示，拉取失败不影响本地列表
    modelApi
      .listCloud()
      .then((data) => {
        if (cancelled) return
        const list = Array.isArray(data) ? data : (data?.items || [])
        const map = {}
        for (const m of list) {
          map[m.model_name] = {
            input: m.input_price_per_call || 0,
            output: m.price_per_call || 0,
            perGen: m.price_per_generation || 0,
          }
        }
        setCloudPrices(map)
      })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

  const PriceNumber = ({ value }) => (
    <span className="text-sm font-bold text-[#2563EB]">{value}</span>
  )

  // 价格徽标：Ele Agent 代理模型展示云端价格；BYOK 标 Local 不标价格
  const PriceBadge = ({ m }) => {
    if (!m.cloud_proxy) {
      return <span className="text-xs font-medium px-2 py-0.5 rounded-full bg-gray-100 text-gray-500">Local</span>
    }
    const price = cloudPrices[m.model_name]
    if (!price) {
      return <span className="text-xs text-eleball-text-secondary">云端计费</span>
    }
    const hasPrice = price.input > 0 || price.output > 0 || price.perGen > 0
    if (!hasPrice) {
      return <span className="text-xs text-eleball-success font-medium">限时免费</span>
    }
    return (
      <span className="text-xs text-eleball-text-secondary">
        {price.input > 0 && <><PriceNumber value={price.input} /> 弹丸/M input</>}
        {price.input > 0 && price.output > 0 && ' · '}
        {price.output > 0 && <><PriceNumber value={price.output} /> 弹丸/M output</>}
        {price.perGen > 0 && (price.input > 0 || price.output > 0) && ' · '}
        {price.perGen > 0 && <><PriceNumber value={price.perGen} /> 弹丸/次</>}
      </span>
    )
  }

  const providers = useMemo(() => {
    const set = new Set(models.map((m) => m.provider))
    return ['全部', ...Array.from(set).sort()]
  }, [models])

  const filtered = useMemo(() => {
    return models.filter((m) => {
      const matchProvider = selectedProvider === '全部' || m.provider === selectedProvider
      const key = keyword.trim().toLowerCase()
      const matchKeyword =
        !key ||
        (m.display_name || '').toLowerCase().includes(key) ||
        (m.model_name || '').toLowerCase().includes(key) ||
        (m.provider || '').toLowerCase().includes(key)
      return matchProvider && matchKeyword
    })
  }, [models, selectedProvider, keyword])

  return (
    <div className="pt-8 pb-16 px-4 max-w-6xl mx-auto">
      <PageHero
        title="模型中心"
        subtitle="Ele Agent 及本地配置的大模型。想用自己的 API Key，可在对话窗口添加自定义配置模型。"
      />

      {/* 筛选与搜索 */}
      <div className="flex flex-col md:flex-row md:items-center gap-4 mb-6">
        <div className="flex flex-wrap gap-2">
          {providers.map((p) => (
            <button
              key={p}
              onClick={() => setSelectedProvider(p)}
              className={`px-3 py-1.5 rounded-full text-xs font-medium transition-colors ${
                selectedProvider === p
                  ? 'bg-eleball-primary text-white'
                  : 'bg-eleball-surface-variant text-eleball-text-secondary hover:bg-eleball-primary-light hover:text-eleball-primary'
              }`}
            >
              {p}
            </button>
          ))}
        </div>
        <div className="relative flex-1 md:max-w-xs ml-auto">
          <Search className="w-4 h-4 text-eleball-text-tertiary absolute left-3 top-1/2 -translate-y-1/2" />
          <input
            type="text"
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="搜索模型名…"
            className="input w-full pl-9 py-2 text-sm"
          />
        </div>
      </div>

      {loading && <p className="text-center text-sm text-eleball-text-secondary py-12">加载中…</p>}

      {error && (
        <div className="flex items-center justify-center gap-2 text-sm text-eleball-error bg-red-50 rounded-xl px-4 py-3 mb-6">
          <AlertCircle className="w-4 h-4" />
          {error}
        </div>
      )}

      {!loading && !error && filtered.length === 0 && (
        <p className="text-center text-sm text-eleball-text-secondary py-12">没有找到符合条件的模型。</p>
      )}

      {/* 模型卡片 */}
      {!loading && (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {filtered.map((m) => (
            <div
              key={`${m.provider}/${m.model_name}`}
              className="card p-4 hover:border-eleball-primary/40 transition-colors"
            >
              <div className="flex items-center justify-between mb-3">
                <span className="text-xs font-medium px-2 py-0.5 rounded-full bg-eleball-primary-light text-eleball-primary">
                  {m.provider}
                </span>
                <PriceBadge m={m} />
              </div>
              <h3 className="font-semibold text-eleball-text mb-1 truncate" title={m.model_name}>
                {m.display_name || m.model_name}
              </h3>
              <p className="text-xs text-eleball-text-tertiary truncate" title={m.model_name}>
                {m.model_name}
              </p>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
