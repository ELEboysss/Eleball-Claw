import useSEO from '../hooks/useSEO'
import CloudFrame from '../components/CloudFrame'

// claw 充值页：内嵌云端 eleball.cn/recharge（充值/支付/VIP/兑换码统一走云端账户）。
// 整页不跳转，URL 仍停留本地。注意：claw 登录态 ≠ 云端 web 登录态（跨域不共享），
// 首次会在内嵌页内看到云端登录墙；在内嵌页内登录一次云端后即可正常充值。
// 见 docs/marketing/claw-implementation-plan.md §C.2。
export default function Recharge() {
  useSEO('充值', 'Eleball 充值 / VIP / 兑换码')
  return <CloudFrame path="/recharge" iframeTitle="Eleball 充值" />
}
