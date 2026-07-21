import { useEffect } from 'react'
import useSEO from '../hooks/useSEO'
import { CLOUD_BASE } from '../api/client'
import { CreditCard, ArrowRight } from 'lucide-react'

// claw 充值页：整页跳转云端 eleball.cn/recharge（充值/支付/VIP/兑换码统一走云端账户）。
// 见 docs/marketing/claw-implementation-plan.md §C.2。
export default function Recharge() {
  useSEO('充值', '跳转至云端充值')
  useEffect(() => {
    window.location.replace(`${CLOUD_BASE}/recharge`)
  }, [])
  return (
    <div className="flex-1 flex items-center justify-center px-4 py-24">
      <div className="text-center max-w-md">
        <CreditCard className="w-10 h-10 text-eleball-primary mx-auto mb-4" />
        <h1 className="text-xl font-bold text-eleball-text mb-2">正在跳转到充值</h1>
        <p className="text-sm text-eleball-text-secondary mb-6">
          充值、VIP 与兑换码统一由云端 eleball.cn 处理。若未自动跳转，请点击下方按钮。
        </p>
        <a
          href={`${CLOUD_BASE}/recharge`}
          className="btn-primary text-sm px-5 py-2 inline-flex items-center gap-2"
        >
          前往充值 <ArrowRight className="w-4 h-4" />
        </a>
      </div>
    </div>
  )
}
