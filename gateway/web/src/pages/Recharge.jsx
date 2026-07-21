import { useState, useEffect, useMemo, useRef } from 'react'
import useSEO from '../hooks/useSEO'
import { Zap, CreditCard, Wallet, History, Minus, Plus, Ticket, Crown } from 'lucide-react'
import QRCode from 'qrcode'
import { useAuth } from '../context/AuthContext'
import { billingApi, rechargeApi, cdkApi, publicSettingApi, vipApi, paymentApi } from '../api/client'
import LoginModal from '../components/LoginModal'
import AlipayCashierModal from '../components/AlipayCashierModal'
import xianyuIcon from '../assets/xianyu-icon.png'
import taobaoIcon from '../assets/taobao-icon.png'

function formatPriceYuan(fen) {
  return (fen / 100).toFixed(2)
}

function formatDateTime(iso) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
}

function sourceTypeLabel(sourceType) {
  switch (sourceType) {
    case 'cdk': return '兑换码充值'
    case 'wechat': return '微信支付'
    case 'alipay': return '支付宝支付'
    case 'manual': return '系统赠送'
    default: return '充值'
  }
}

// 支付入口开关：仅本地开发/E2E（start-local.ps1 或 Playwright）注入 VITE_PAYMENT_ENABLED=true 时放开；
// 生产构建不含该变量，支付按钮保持「暂未开放」禁用态
const PAYMENT_ENTRY_ENABLED = import.meta.env.VITE_PAYMENT_ENABLED === 'true'

export default function Recharge() {
  useSEO('弹丸充值与 VIP', '自带 Key 免费；Ele Agent 按 token 扣弹丸；VIP 解锁 Agent 模式。')
  const { isLoggedIn, user } = useAuth()
  const [balance, setBalance] = useState(null)
  const [packages, setPackages] = useState([])
  const [packagesLoading, setPackagesLoading] = useState(true)
  const [selectedId, setSelectedId] = useState(null)
  const [quantity, setQuantity] = useState(1)
  const [loginOpen, setLoginOpen] = useState(false)
  const [message, setMessage] = useState('')
  const [cdkCode, setCdkCode] = useState('')
  const [cdkLoading, setCdkLoading] = useState(false)
  const [xianyuUrl, setXianyuUrl] = useState('')
  const [taobaoUrl, setTaobaoUrl] = useState('')
  const [history, setHistory] = useState([])
  const [historyLoading, setHistoryLoading] = useState(false)

  // VIP 会员状态
  const [vipStatus, setVipStatus] = useState(null)
  const [vipPlans, setVipPlans] = useState([])
  const [selectedVipId, setSelectedVipId] = useState(null)
  const [useElegantForVIP, setUseElegantForVIP] = useState(false)
  const [vipLoading, setVipLoading] = useState(false)

  // 支付宝收银台状态
  const [payLoading, setPayLoading] = useState(false)
  const [cashier, setCashier] = useState(null) // { orderId, amountFen, productType, qrDataUrl }
  const [cashierPaid, setCashierPaid] = useState(false)
  const pollTimerRef = useRef(null)

  // 停止订单状态轮询
  const stopOrderPolling = () => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }

  // 支付成功后刷新页面数据（余额 / 充值记录 / VIP 状态）
  const refreshAfterPaid = (productType) => {
    loadBalance()
    loadHistory()
    if (productType === 'vip') {
      vipApi.getStatus().then((s) => setVipStatus(s)).catch(() => {})
    }
  }

  // 打开收银台：渲染二维码并开始 2s 轮询订单状态
  const openCashier = async ({ orderId, amountFen, productType, qrCode }) => {
    const qrDataUrl = await QRCode.toDataURL(qrCode, { width: 448, margin: 1 })
    setCashierPaid(false)
    setCashier({ orderId, amountFen, productType, qrDataUrl })

    stopOrderPolling()
    pollTimerRef.current = setInterval(async () => {
      try {
        const data = await paymentApi.getOrderStatus(orderId)
        if (data?.status === 'paid') {
          stopOrderPolling()
          setCashierPaid(true)
          refreshAfterPaid(productType)
        }
      } catch (err) {
        // 单次轮询失败静默重试（网络抖动），不打断收银台
        console.warn('轮询订单状态失败', err)
      }
    }, 2000)
  }

  // 关闭收银台：停止轮询；未支付的订单保留 pending，可重新发起继续支付
  const closeCashier = () => {
    stopOrderPolling()
    setCashier(null)
    setCashierPaid(false)
  }

  // 组件卸载时清理轮询定时器
  useEffect(() => stopOrderPolling, [])

  // 弹丸充值：支付宝扫码支付
  const handleAlipayPay = async () => {
    if (!isLoggedIn) {
      setLoginOpen(true)
      return
    }
    if (!selectedId) {
      setMessage('请选择充值套餐')
      return
    }
    setPayLoading(true)
    setMessage('')
    try {
      const data = await paymentApi.alipayPrecreate({ package_id: selectedId, quantity })
      await openCashier({
        orderId: data.order_id,
        amountFen: data.amount_fen,
        productType: 'recharge',
        qrCode: data.qr_code
      })
    } catch (err) {
      setMessage(err.message || '创建支付订单失败')
    } finally {
      setPayLoading(false)
    }
  }

  const loadBalance = () => {
    billingApi.getBalance()
      .then((data) => setBalance(data))
      .catch(() => setBalance(null))
  }

  const loadHistory = () => {
    setHistoryLoading(true)
    billingApi.getRechargeHistory(1, 50)
      .then((data) => setHistory(data?.items || []))
      .catch(() => setHistory([]))
      .finally(() => setHistoryLoading(false))
  }

  useEffect(() => {
    if (!isLoggedIn) return
    loadBalance()
    loadHistory()
  }, [isLoggedIn])

  useEffect(() => {
    if (!isLoggedIn) return
    // 加载 VIP 状态与套餐
    vipApi.getStatus().then((data) => setVipStatus(data)).catch(() => setVipStatus(null))
    vipApi.listPlans()
      .then((data) => {
        const items = data?.items || []
        setVipPlans(items)
        if (items.length > 0) {
          setSelectedVipId(items[0].id)
        }
      })
      .catch(() => setVipPlans([]))
  }, [isLoggedIn])

  useEffect(() => {
    if (!isLoggedIn) return
    publicSettingApi.get()
      .then((data) => {
        setXianyuUrl(data?.xianyu_product_url || '')
        setTaobaoUrl(data?.taobao_product_url || '')
      })
      .catch(() => {
        setXianyuUrl('')
        setTaobaoUrl('')
      })

    setPackagesLoading(true)
    rechargeApi.listPackages()
      .then((data) => {
        const items = data?.items || []
        setPackages(items)
        if (items.length > 0) {
          // 默认选中第一个固定套餐；如果没有固定套餐则选第一个
          const defaultItem = items.find((p) => !p.is_custom_multiplier) || items[0]
          setSelectedId(defaultItem.id)
        }
      })
      .catch((err) => {
        console.error('加载充值套餐失败', err)
        setMessage('加载充值套餐失败')
      })
      .finally(() => setPackagesLoading(false))
  }, [isLoggedIn])

  const selectedPackage = useMemo(
    () => packages.find((p) => p.id === selectedId) || null,
    [packages, selectedId]
  )

  const selectedVipPlan = useMemo(
    () => vipPlans.find((p) => p.id === selectedVipId) || null,
    [vipPlans, selectedVipId]
  )

  // 选中 VIP 套餐时，优雅弹丸可抵扣金额（不超过套餐价格）
  const elegantDeductionFen = useMemo(() => {
    if (!useElegantForVIP || !balance || !selectedVipPlan) return 0
    return Math.min(balance.elegant, selectedVipPlan.price_fen)
  }, [useElegantForVIP, balance, selectedVipPlan])

  // 计算当前选中套餐的实际总价与总弹丸数
  const resolved = useMemo(() => {
    if (!selectedPackage) return null
    if (selectedPackage.is_custom_multiplier && selectedPackage.base_package) {
      const base = selectedPackage.base_package
      return {
        danwan: base.danwan * quantity,
        priceFen: base.price_fen * quantity,
        label: `${selectedPackage.name}：${base.name} × ${quantity}`
      }
    }
    return {
      danwan: selectedPackage.danwan,
      priceFen: selectedPackage.price_fen,
      label: selectedPackage.name
    }
  }, [selectedPackage, quantity])

  const handleQuantityChange = (delta) => {
    setQuantity((q) => Math.max(1, q + delta))
  }

  const handleSubscribeVIP = async () => {
    if (!isLoggedIn) {
      setLoginOpen(true)
      return
    }
    if (!selectedVipId) {
      setMessage('请选择 VIP 套餐')
      return
    }
    setVipLoading(true)
    setMessage('')
    try {
      const data = await vipApi.subscribe(selectedVipId, 'alipay', useElegantForVIP)
      loadBalance()
      vipApi.getStatus().then((s) => setVipStatus(s)).catch(() => {})
      if (data.cash_amount === 0) {
        setMessage(`已使用优雅弹丸抵扣 ¥${formatPriceYuan(data.elegant_deducted)}，VIP 开通成功`)
      } else {
        // 有现金部分：调支付宝预下单并打开收银台扫码支付
        const payData = await paymentApi.alipayPrecreate({ order_id: data.order_id })
        await openCashier({
          orderId: payData.order_id,
          amountFen: payData.amount_fen,
          productType: 'vip',
          qrCode: payData.qr_code
        })
      }
    } catch (err) {
      setMessage(err.message || '订阅失败')
    } finally {
      setVipLoading(false)
    }
  }

  const handleRedeemCDK = async () => {
    if (!isLoggedIn) {
      setLoginOpen(true)
      return
    }
    const code = cdkCode.trim()
    if (!code) {
      setMessage('请输入兑换码')
      return
    }
    setCdkLoading(true)
    setMessage('')
    try {
      const data = await cdkApi.redeem(code)
      setCdkCode('')
      if (data.vip_activated) {
        setMessage(`兑换成功！已激活 VIP${data.vip_level}，有效期至 ${formatDateTime(data.vip_expire_at)}`)
        // 刷新 VIP 状态
        vipApi.getStatus().then((s) => setVipStatus(s)).catch(() => {})
      } else {
        setMessage(`兑换成功！获得 ${(data.danwan || data.value).toLocaleString('zh-CN')} 弹丸`)
      }
      // 刷新余额与充值记录
      loadBalance()
      loadHistory()
    } catch (err) {
      setMessage(err.message || '兑换失败')
    } finally {
      setCdkLoading(false)
    }
  }

  if (!isLoggedIn) {
    return (
      <div className="pt-24 px-4 text-center">
        <div className="max-w-md mx-auto card">
          <Wallet className="w-12 h-12 text-eleball-primary mx-auto mb-4" />
          <h2 className="text-xl font-bold text-eleball-text mb-2">登录后充值</h2>
          <p className="text-sm text-eleball-text-secondary mb-6">充值前需要先登录 Eleball 账号。</p>
          <button onClick={() => setLoginOpen(true)} className="btn-primary w-full justify-center">
            登录 / 注册
          </button>
        </div>
        <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />
      </div>
    )
  }

  return (
    <div className="pt-24 pb-16 px-4">
      <div className="max-w-2xl mx-auto">
        <div className="text-center mb-10">
          <h1 className="text-3xl font-bold text-eleball-text mb-2">Token 充值</h1>
          <p className="text-eleball-text-secondary">为 Eleball 账号充值弹丸，按量消费</p>
        </div>

        {/* Balance Card */}
        <div className="card mb-8 border-eleball-primary-light">
          <div className="flex items-center gap-2 mb-3">
            <Zap className="w-5 h-5 text-eleball-primary" />
            <span className="font-medium text-eleball-text-secondary">当前余额</span>
          </div>
          <div className="grid grid-cols-2 gap-4">
            <div>
              <div className="text-3xl font-bold text-eleball-text">
                {balance ? balance.danwan.toLocaleString('zh-CN') : '—'}
              </div>
              <div className="text-sm text-eleball-text-secondary mt-0.5">弹丸</div>
            </div>
            <div>
              <div className="text-3xl font-bold text-eleball-text">
                {balance ? balance.elegant.toLocaleString('zh-CN') : '—'}
              </div>
              <div className="text-sm text-eleball-text-secondary mt-0.5">优雅弹丸</div>
            </div>
          </div>
        </div>

        {/* VIP Membership */}
        <div className="card mb-8 border-amber-200">
          <h2 className="text-lg font-semibold text-eleball-text flex items-center gap-2 mb-4">
            <Crown className="w-5 h-5 text-amber-500" />
            弹丸计划 VIP
          </h2>

          {vipStatus && (
            <div className="mb-4 p-3 rounded-xl bg-amber-50 text-sm">
              {user?.role === 'admin' ? (
                <div>
                  <span className="font-semibold text-amber-700">当前 管理员</span>
                  <p className="text-amber-700/70 mt-0.5">享有全部能力，无额度限制</p>
                </div>
              ) : vipStatus.is_vip ? (
                <div className="flex items-center justify-between flex-wrap gap-2">
                  <div>
                    <span className="font-semibold text-amber-700">当前 VIP{vipStatus.level}</span>
                    <span className="text-amber-700/80 ml-2">{vipStatus.plan_name}</span>
                    <p className="text-amber-700/70 mt-0.5">
                      有效期至 {formatDateTime(vipStatus.expire_at)} · 调用单价 {vipStatus.discount_percent}%
                    </p>
                  </div>
                </div>
              ) : (
                <div>
                  <span className="font-semibold text-amber-700">当前 小弹丸</span>
                  <p className="text-amber-700/70 mt-0.5">
                    新用户默认等级，可免费试用 Agent 模式 {vipStatus.agent_trial_remaining ?? 3} 次
                  </p>
                </div>
              )}
            </div>
          )}

          {vipPlans.length === 0 ? (
            <p className="text-sm text-eleball-text-secondary">暂无可用 VIP 套餐</p>
          ) : (
            <>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-4">
                {vipPlans.map((plan) => {
                  const isSelected = selectedVipId === plan.id
                  return (
                    <div
                      key={plan.id}
                      onClick={() => setSelectedVipId(plan.id)}
                      className={`cursor-pointer border-2 rounded-xl p-4 transition-all ${
                        isSelected
                          ? 'border-amber-400 bg-amber-50/50'
                          : 'border-transparent bg-white hover:border-eleball-outline-variant'
                      }`}
                    >
                      <div className="flex items-center justify-between mb-1">
                        <h3 className="font-semibold text-eleball-text">{plan.name}</h3>
                        <span className="text-xs px-2 py-0.5 rounded-full bg-amber-100 text-amber-700">VIP{plan.level}</span>
                      </div>
                      <p className="text-2xl font-bold text-amber-600">¥{formatPriceYuan(plan.price_fen)}<span className="text-sm font-normal text-eleball-text-secondary">/{plan.duration_days}天</span></p>
                      <p className="text-xs text-eleball-text-secondary mt-2">
                        {plan.agent_enabled ? 'Agent 模式' : ''}
                        {plan.agent_enabled && plan.file_tools_enabled ? ' · ' : ''}
                        {plan.file_tools_enabled ? '文件工具' : ''}
                        {plan.discount_percent < 100 ? ` · 调用单价 ${plan.discount_percent}%` : ''}
                      </p>
                      {plan.description && (
                        <p className="text-xs text-eleball-text-tertiary mt-1">{plan.description}</p>
                      )}
                    </div>
                  )
                })}
              </div>

              {balance && balance.elegant > 0 && selectedVipPlan && (
                <label className="flex items-center gap-2 mb-3 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={useElegantForVIP}
                    onChange={(e) => setUseElegantForVIP(e.target.checked)}
                    className="w-5 h-5 rounded border-eleball-outline text-amber-500 focus:ring-amber-500"
                  />
                  <span className="text-sm text-eleball-text">
                    使用优雅弹丸抵扣（可用 {balance.elegant.toLocaleString('zh-CN')}，1:1 抵扣现金）
                  </span>
                </label>
              )}

              {selectedVipPlan && (
                <div className="text-sm text-eleball-text-secondary mb-4 space-y-1">
                  <div>
                    已选 <span className="font-medium text-eleball-text">{selectedVipPlan.name}</span>
                  </div>
                  <div className="flex justify-between">
                    <span>套餐金额</span>
                    <span className="font-medium text-eleball-text">¥{formatPriceYuan(selectedVipPlan.price_fen)}</span>
                  </div>
                  {elegantDeductionFen > 0 && (
                    <div className="flex justify-between text-amber-600">
                      <span>优雅弹丸抵扣</span>
                      <span className="font-medium">- ¥{formatPriceYuan(elegantDeductionFen)}</span>
                    </div>
                  )}
                  <div className="flex justify-between pt-1 border-t border-eleball-outline">
                    <span className="font-medium text-eleball-text">实付款额</span>
                    <span className="font-bold text-amber-600">
                      ¥{formatPriceYuan(selectedVipPlan.price_fen - elegantDeductionFen)}
                      {elegantDeductionFen > 0 && (
                        <span className="text-xs font-normal text-eleball-text-secondary ml-1">
                          （优雅弹丸抵扣¥{formatPriceYuan(elegantDeductionFen)}）
                        </span>
                      )}
                    </span>
                  </div>
                </div>
              )}

              <div className="flex flex-col gap-3">
                {PAYMENT_ENTRY_ENABLED ? (
                  <button
                    onClick={handleSubscribeVIP}
                    disabled={vipLoading || !selectedVipPlan}
                    className="btn-primary justify-center disabled:opacity-60"
                  >
                    {vipLoading ? '创建订单中...' : '支付宝开通 / 续期'}
                  </button>
                ) : (
                  <button
                    disabled={true}
                    className="btn-primary justify-center opacity-50 cursor-not-allowed"
                  >
                    暂未开放
                  </button>
                )}
                <p className="text-xs text-eleball-text-secondary">
                  提示：更换套餐时，旧套餐剩余完整周数将按「月卡价 × 剩余周数 / 4」退还优雅弹丸，可用于购买服务。
                </p>
              </div>
            </>
          )}
        </div>

        {/* Packages */}
        <div className="space-y-4 mb-8">
          <h2 className="text-lg font-semibold text-eleball-text flex items-center gap-2">
            <CreditCard className="w-5 h-5 text-eleball-primary" />
            选择充值套餐
          </h2>

          {packagesLoading && (
            <div className="text-sm text-eleball-text-secondary">加载套餐中...</div>
          )}

          {!packagesLoading && packages.length === 0 && (
            <div className="text-sm text-eleball-text-secondary">暂无可用充值套餐</div>
          )}

          {packages.map((pkg) => {
            const isCustom = pkg.is_custom_multiplier
            const base = pkg.base_package
            const isSelected = selectedId === pkg.id
            const displayDanwan = isCustom && base ? base.danwan * quantity : pkg.danwan
            const displayPriceFen = isCustom && base ? base.price_fen * quantity : pkg.price_fen
            const displayName = isCustom && base
              ? `${pkg.name}：${base.name} × ${quantity}`
              : pkg.name

            return (
              <div
                key={pkg.id}
                onClick={() => {
                  if (selectedId !== pkg.id) {
                    setSelectedId(pkg.id)
                    if (isCustom) setQuantity(1)
                  }
                }}
                className={`card cursor-pointer border-2 transition-all ${
                  isSelected
                    ? 'border-eleball-primary bg-eleball-primary-light/30'
                    : 'border-transparent hover:border-eleball-outline-variant'
                }`}
              >
                <div className="flex items-center justify-between gap-4">
                  <div className="flex-1 min-w-0">
                    <h3 className="text-lg font-semibold text-eleball-text">{displayName}</h3>
                    <p className="text-sm text-eleball-text-secondary">
                      {displayDanwan.toLocaleString('zh-CN')} 弹丸
                    </p>
                    {pkg.description && (
                      <p className="text-xs text-eleball-text-tertiary mt-1">{pkg.description}</p>
                    )}
                  </div>
                  <div className="flex items-center gap-4">
                    {isCustom && base && (
                      <div
                        className="flex items-center gap-2"
                        onClick={(e) => e.stopPropagation()}
                      >
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation()
                            handleQuantityChange(-1)
                          }}
                          disabled={quantity <= 1}
                          className="w-8 h-8 rounded-lg border border-eleball-outline flex items-center justify-center text-eleball-text hover:bg-eleball-primary-light disabled:opacity-40"
                        >
                          <Minus className="w-4 h-4" />
                        </button>
                        <span className="w-8 text-center font-medium text-eleball-text">{quantity}</span>
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation()
                            handleQuantityChange(1)
                          }}
                          className="w-8 h-8 rounded-lg border border-eleball-outline flex items-center justify-center text-eleball-text hover:bg-eleball-primary-light"
                        >
                          <Plus className="w-4 h-4" />
                        </button>
                      </div>
                    )}
                    <div className="text-2xl font-bold text-eleball-primary">
                      ¥{formatPriceYuan(displayPriceFen)}
                    </div>
                  </div>
                </div>
              </div>
            )
          })}
        </div>

        {/* Pay Buttons */}
        <div className="grid grid-cols-1 gap-4 mb-8">
          {PAYMENT_ENTRY_ENABLED ? (
            <button
              onClick={handleAlipayPay}
              disabled={payLoading || !selectedPackage}
              className="btn-secondary justify-center disabled:opacity-60"
            >
              {payLoading ? '创建订单中...' : '支付宝支付'}
            </button>
          ) : (
            <button
              disabled={true}
              className="btn-secondary justify-center disabled:opacity-60 cursor-not-allowed"
              title="产品内测，支付宝支付暂未开放"
            >
              支付宝（产品内测，暂未开放）
            </button>
          )}
        </div>

        {resolved && (
          <div className="text-sm text-eleball-text-secondary mb-6 text-center">
            已选：<span className="font-medium text-eleball-text">{resolved.label}</span>，
            共 <span className="font-medium text-eleball-text">{resolved.danwan.toLocaleString('zh-CN')} 弹丸</span>，
            应付 <span className="font-medium text-eleball-primary">¥{formatPriceYuan(resolved.priceFen)}</span>
          </div>
        )}

        {/* CDK Redemption */}
        <div className="card mb-8 border-eleball-primary-light">
          <h2 className="text-lg font-semibold text-eleball-text flex items-center gap-2 mb-4">
            <Ticket className="w-5 h-5 text-eleball-primary" />
            兑换码充值
          </h2>
          <div className="flex flex-col sm:flex-row gap-3">
            <input
              type="text"
              value={cdkCode}
              onChange={(e) => setCdkCode(e.target.value)}
              placeholder="请输入 16 位兑换码"
              className="input flex-1"
              maxLength={20}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleRedeemCDK()
              }}
            />
            <button
              onClick={handleRedeemCDK}
              disabled={cdkLoading || !cdkCode.trim()}
              className="btn-primary justify-center whitespace-nowrap disabled:opacity-60"
            >
              {cdkLoading ? '兑换中...' : '兑换'}
            </button>
          </div>
          <p className="text-xs text-eleball-text-secondary mt-2">
            输入兑换码即可充值对应面额的弹丸，每个兑换码只能使用一次。
          </p>

          <div className="mt-4 flex flex-wrap gap-3">
            {taobaoUrl && (
              <a
                href={taobaoUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 px-5 py-2.5 rounded-full border border-eleball-outline bg-white text-eleball-text hover:bg-eleball-surface transition-colors text-sm font-medium shadow-sm"
              >
                <img src={taobaoIcon} alt="淘宝" className="w-5 h-5 object-contain" />
                <span>淘宝链接</span>
              </a>
            )}
            {xianyuUrl && (
              <a
                href={xianyuUrl}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex items-center gap-2 px-5 py-2.5 rounded-full border border-eleball-outline bg-white text-eleball-text hover:bg-eleball-surface transition-colors text-sm font-medium shadow-sm"
              >
                <img src={xianyuIcon} alt="闲鱼" className="w-5 h-5 object-contain" />
                <span>闲鱼链接</span>
              </a>
            )}
          </div>
        </div>

        {message && (
          <div className={`text-sm rounded-xl px-4 py-3 mb-8 ${
            message.includes('失败') || message.includes('请输入') ? 'text-eleball-error bg-red-50' : 'text-eleball-primary bg-eleball-primary-light/50'
          }`}>
            {message}
          </div>
        )}

        {/* Records */}
        <div className="card">
          <h2 className="text-lg font-semibold text-eleball-text flex items-center gap-2 mb-4">
            <History className="w-5 h-5 text-eleball-primary" />
            充值记录
          </h2>

          {historyLoading && (
            <p className="text-sm text-eleball-text-secondary">加载中...</p>
          )}

          {!historyLoading && history.length === 0 && (
            <p className="text-sm text-eleball-text-secondary">暂无充值记录</p>
          )}

          {!historyLoading && history.length > 0 && (
            <ul className="space-y-3">
              {history.map((item) => (
                <li
                  key={item.id}
                  className="flex items-center justify-between gap-4 py-2 border-b border-eleball-outline-variant last:border-0"
                >
                  <div className="min-w-0">
                    <p className="text-sm font-medium text-eleball-text">
                      {sourceTypeLabel(item.source_type)}
                    </p>
                    <p className="text-xs text-eleball-text-secondary">
                      {formatDateTime(item.created_at)}
                    </p>
                  </div>
                  <div className="text-base font-semibold text-eleball-primary whitespace-nowrap">
                    +{item.amount.toLocaleString('zh-CN')} 弹丸
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {/* 支付宝收银台模态：充值与 VIP 开通复用 */}
      <AlipayCashierModal open={!!cashier} cashier={cashier} paid={cashierPaid} onClose={closeCashier} />
    </div>
  )
}
