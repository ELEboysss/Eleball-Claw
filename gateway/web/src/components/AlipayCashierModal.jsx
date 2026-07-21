import { X, CheckCircle2, Loader2 } from 'lucide-react'

function formatPriceYuan(fen) {
  return (fen / 100).toFixed(2)
}

/**
 * 支付宝收银台模态框：展示扫码二维码并轮询订单状态。
 * 充值与 VIP 开通两入口复用；关闭即停止轮询，订单保留 pending 可重新打开继续支付。
 *
 * Props:
 * - open: 是否显示
 * - cashier: { orderId, amountFen, productType, qrDataUrl }
 * - paid: 是否已支付成功（轮询确认后置为 true）
 * - onClose: 关闭回调
 */
export default function AlipayCashierModal({ open, cashier, paid, onClose }) {
  if (!open || !cashier) return null

  const subject = cashier.productType === 'vip' ? 'Eleball VIP 套餐' : 'Eleball 弹丸充值'

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
      <div className="card w-full max-w-sm relative">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 text-eleball-text-secondary hover:text-eleball-text"
          aria-label="关闭"
        >
          <X className="w-5 h-5" />
        </button>

        {paid ? (
          <div className="text-center py-6">
            <CheckCircle2 className="w-14 h-14 text-green-500 mx-auto mb-4" />
            <h3 className="text-xl font-bold text-eleball-text mb-2">支付成功</h3>
            <p className="text-sm text-eleball-text-secondary mb-1">{subject}</p>
            <p className="text-lg font-semibold text-eleball-primary mb-6">¥{formatPriceYuan(cashier.amountFen)}</p>
            <button onClick={onClose} className="btn-primary w-full justify-center">
              完成
            </button>
          </div>
        ) : (
          <div className="text-center">
            <h3 className="text-lg font-bold text-eleball-text mb-1">支付宝扫码支付</h3>
            <p className="text-sm text-eleball-text-secondary mb-4">{subject}</p>

            <div className="flex justify-center mb-4">
              {cashier.qrDataUrl ? (
                <img src={cashier.qrDataUrl} alt="支付宝支付二维码" className="w-56 h-56 rounded-xl border border-eleball-outline" />
              ) : (
                <div className="w-56 h-56 rounded-xl border border-eleball-outline flex items-center justify-center">
                  <Loader2 className="w-8 h-8 animate-spin text-eleball-primary" />
                </div>
              )}
            </div>

            <p className="text-2xl font-bold text-eleball-primary mb-3">¥{formatPriceYuan(cashier.amountFen)}</p>

            <div className="flex items-center justify-center gap-2 text-sm text-eleball-text-secondary mb-2">
              <Loader2 className="w-4 h-4 animate-spin" />
              <span>等待支付结果确认中...</span>
            </div>
            <p className="text-xs text-eleball-text-tertiary">
              请使用支付宝 App 扫码完成支付；二维码 30 分钟内有效，关闭后可在充值页重新发起。
            </p>
          </div>
        )}
      </div>
    </div>
  )
}
