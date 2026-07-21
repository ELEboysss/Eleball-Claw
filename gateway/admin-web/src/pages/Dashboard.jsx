import { useEffect, useState, useMemo } from 'react'
import { AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, BarChart, Bar } from 'recharts'
import { dashboardApi } from '../api/client'

// 将后端日期字符串（如 "2026-06-23"）转换为图表展示用的 MM-DD
function formatChartDate(dateStr) {
  if (!dateStr) return ''
  const d = new Date(dateStr)
  if (isNaN(d.getTime())) {
    return dateStr.length >= 10 ? dateStr.slice(5, 10) : dateStr
  }
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${m}-${day}`
}

// 数字格式化：12345 -> 12.3K
function formatNumber(num) {
  if (num === undefined || num === null) return '-'
  const n = Number(num)
  if (n >= 100000000) return (n / 100000000).toFixed(1) + '亿'
  if (n >= 10000) return (n / 10000).toFixed(1) + '万'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return String(n)
}

// 日期时间格式化
function formatDateTime(isoStr) {
  if (!isoStr) return '-'
  const d = new Date(isoStr)
  return isNaN(d.getTime()) ? isoStr : d.toLocaleString('zh-CN')
}

// 金额格式化（分 -> 元）
function formatMoney(cents) {
  if (cents === undefined || cents === null) return '-'
  return '¥' + (Number(cents) / 100).toFixed(2)
}

// 计算环比：((today - yesterday) / yesterday) * 100，保留 1 位小数
function growthRate(today, yesterday) {
  const t = Number(today) || 0
  const y = Number(yesterday) || 0
  if (y === 0) {
    return t > 0 ? '+100%' : '0%'
  }
  const rate = ((t - y) / y) * 100
  const sign = rate >= 0 ? '+' : ''
  return `${sign}${rate.toFixed(1)}%`
}

const activityColor = (type) => {
  switch (type) {
    case 'user_registered': return 'bg-blue-50 text-blue-600'
    case 'user_recharged': return 'bg-emerald-50 text-emerald-600'
    default: return 'bg-gray-50 text-gray-600'
  }
}

// 解析动态的 metadata JSON，安全失败时返回空对象
function parseMetadata(metadata) {
  if (!metadata) return {}
  try {
    return JSON.parse(metadata)
  } catch {
    return {}
  }
}

// 从 metadata 中提取 input / output tokens，兼容旧数据只有 total_tokens 的情况
function formatTokens(metadata) {
  const meta = parseMetadata(metadata)
  const input = Number(meta.input_tokens) || 0
  const output = Number(meta.output_tokens) || 0
  const total = Number(meta.total_tokens) || Number(meta.tokens) || 0
  if (input > 0 || output > 0) {
    return `输入 ${input.toLocaleString()} / 输出 ${output.toLocaleString()} tokens`
  }
  if (total > 0) {
    return `${total.toLocaleString()} tokens`
  }
  return null
}

export default function Dashboard() {
  const [stats, setStats] = useState(null)
  const [dauData, setDauData] = useState([])
  const [tokenData, setTokenData] = useState([])
  const [activities, setActivities] = useState([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const fetchAll = async () => {
      setLoading(true)
      try {
        const [statsRes, dauRes, tokenRes, activitiesRes] = await Promise.all([
          dashboardApi.getStats(),
          dashboardApi.getDailyActive(7),
          dashboardApi.getTokenUsage(7),
          dashboardApi.getActivities()
        ])
        setStats(statsRes || {})

        setDauData(
          (dauRes || []).map((item) => ({
            date: formatChartDate(item.date),
            dau: Number(item.value) || 0
          }))
        )

        setTokenData(
          (tokenRes || []).map((item) => ({
            date: formatChartDate(item.date),
            input: Number(item.input) || 0,
            output: Number(item.output) || 0
          }))
        )
        setActivities(activitiesRes || [])
      } catch (err) {
        console.error('加载仪表盘数据失败', err)
      } finally {
        setLoading(false)
      }
    }

    fetchAll()
  }, [])

  // 统计卡片根据后端真实数据动态生成，环比使用昨日数据真实计算
  const statsCards = useMemo(() => {
    if (!stats) return []
    return [
      {
        label: '总用户数',
        value: formatNumber(stats.total_users),
        change: null,
        trend: 'neutral'
      },
      {
        label: '今日活跃用户',
        value: formatNumber(stats.today_active),
        change: growthRate(stats.today_active, stats.yesterday_active),
        trend: (stats.today_active || 0) >= (stats.yesterday_active || 0) ? 'up' : 'down'
      },
      {
        label: '今日 Token 消耗',
        value: formatNumber(stats.today_token_usage),
        change: growthRate(stats.today_token_usage, stats.yesterday_token_usage),
        trend: (stats.today_token_usage || 0) >= (stats.yesterday_token_usage || 0) ? 'up' : 'down'
      },
      {
        label: '今日收入',
        value: formatMoney(stats.today_revenue),
        change: growthRate(stats.today_revenue, stats.yesterday_revenue),
        trend: (stats.today_revenue || 0) >= (stats.yesterday_revenue || 0) ? 'up' : 'down'
      }
    ]
  }, [stats])

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold">数据概览</h1>
        <p className="text-eleball-text-secondary mt-1">实时监控平台核心指标</p>
      </div>

      {/* 统计卡片 */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        {statsCards.map((card) => (
          <div key={card.label} className="card">
            <p className="text-sm text-eleball-text-secondary">{card.label}</p>
            <div className="flex items-end gap-2 mt-2">
              <span className="text-2xl font-bold">{card.value}</span>
              {card.change && (
                <span className={`text-xs font-medium mb-1 ${card.trend === 'up' ? 'text-emerald-600' : card.trend === 'down' ? 'text-red-500' : 'text-gray-500'}`}>
                  {card.change}
                </span>
              )}
            </div>
          </div>
        ))}
      </div>

      {/* 图表区域 */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="card">
          <h3 className="text-base font-semibold mb-4">日活跃用户（DAU）</h3>
          <div className="h-64">
            {loading ? (
              <div className="h-full flex items-center justify-center text-eleball-text-secondary">加载中...</div>
            ) : dauData.length === 0 ? (
              <div className="h-full flex items-center justify-center text-eleball-text-secondary">暂无数据</div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={dauData}>
                  <defs>
                    <linearGradient id="colorDau" x1="0" y1="0" x2="0" y2="1">
                      <stop offset="5%" stopColor="#14B8A6" stopOpacity={0.2}/>
                      <stop offset="95%" stopColor="#14B8A6" stopOpacity={0}/>
                    </linearGradient>
                  </defs>
                  <CartesianGrid strokeDasharray="3 3" stroke="#E2E8F0" />
                  <XAxis dataKey="date" tick={{fontSize: 12}} axisLine={false} tickLine={false} />
                  <YAxis tick={{fontSize: 12}} axisLine={false} tickLine={false} allowDecimals={false} />
                  <Tooltip
                    contentStyle={{ borderRadius: '12px', border: 'none', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}
                  />
                  <Area type="monotone" dataKey="dau" stroke="#14B8A6" strokeWidth={2} fillOpacity={1} fill="url(#colorDau)" />
                </AreaChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>

        <div className="card">
          <h3 className="text-base font-semibold mb-4">Token 使用量（输入 / 输出）</h3>
          <div className="h-64">
            {loading ? (
              <div className="h-full flex items-center justify-center text-eleball-text-secondary">加载中...</div>
            ) : tokenData.length === 0 ? (
              <div className="h-full flex items-center justify-center text-eleball-text-secondary">暂无数据</div>
            ) : (
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={tokenData}>
                  <CartesianGrid strokeDasharray="3 3" stroke="#E2E8F0" />
                  <XAxis dataKey="date" tick={{fontSize: 12}} axisLine={false} tickLine={false} />
                  <YAxis tick={{fontSize: 12}} axisLine={false} tickLine={false} allowDecimals={false} />
                  <Tooltip
                    contentStyle={{ borderRadius: '12px', border: 'none', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}
                  />
                  <Bar dataKey="input" fill="#14B8A6" radius={[4, 4, 0, 0]} />
                  <Bar dataKey="output" fill="#8B5CF6" radius={[4, 4, 0, 0]} />
                </BarChart>
              </ResponsiveContainer>
            )}
          </div>
        </div>
      </div>

      {/* 最近动态 */}
      <div className="card">
        <h3 className="text-base font-semibold mb-4">最近动态</h3>
        {activities.length === 0 ? (
          <div className="py-8 text-center text-eleball-text-secondary text-sm">
            暂无动态数据
          </div>
        ) : (
          <div className="space-y-3">
            {activities.map((item) => (
              <div key={item.id} className="flex items-start gap-3 p-3 rounded-xl bg-gray-50/50 hover:bg-gray-50 transition-colors">
                <div className={`p-2 rounded-lg ${activityColor(item.type)} text-xs font-bold`}>
                  {item.type === 'user_registered' ? '注册' : item.type === 'user_recharged' ? '充值' : '动态'}
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium">{item.title}</p>
                  <p className="text-sm text-eleball-text-secondary truncate">{item.description}</p>
                  {item.type === 'model_usage' && formatTokens(item.metadata) && (
                    <p className="text-xs text-eleball-text-secondary mt-1">
                      {formatTokens(item.metadata)}
                    </p>
                  )}
                </div>
                <span className="text-xs text-eleball-text-secondary whitespace-nowrap">
                  {formatDateTime(item.created_at)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
