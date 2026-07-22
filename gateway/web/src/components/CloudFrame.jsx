import { useEffect, useRef, useState } from 'react'
import { CLOUD_BASE } from '../api/client'
import { registerCloudIframe, broadcastCurrentAuth } from '../utils/authSync'
import { Loader2 } from 'lucide-react'

/**
 * 云端内容内嵌：把 eleball.cn 的页面用 iframe 原样拉进本地 claw web。
 *
 * - 浏览器不整页跳转到 eleball.cn，URL 仍停留在本地 claw 域名（满足「不离开本地 web」）。
 * - 内容实时取云端，claw 无需维护本地副本，官网等内容不会与云端漂移。
 * - eleball.cn 无 X-Frame-Options / CSP frame-ancestors 限制，允许被嵌。
 *
 * 用于官网（/）、充值（/recharge）等「非本地功能」内容页；其余云端内容（文档/隐私/条款）
 * 在官网 iframe 内导航到达，不再单独建本地路由。
 * 见 docs/marketing/claw-implementation-plan.md §C.2。
 *
 * sandbox 取舍：
 * - allow-scripts + allow-same-origin：云端 SPA 能正常渲染并读写自己的 localStorage/cookie；
 *   allow-same-origin 同时是登录态同步的前提（postMessage targetOrigin 匹配 + localStorage 可写）。
 * - allow-forms + allow-popups + allow-popups-to-escape-sandbox：登录/支付表单可用，外部链接可新标签打开。
 * - allow-top-navigation-by-user-activation：支付跳转（支付宝/微信）这类用户主动触发的跳出可放行。
 * - 不给 allow-top-navigation：禁止云端页面用脚本自动把整个浏览器跳走（即原 window.location.replace 的行为）。
 *
 * 登录态同步：注册 iframe 后于 onLoad 推送 claw 当前 token；云端 SPA 监听就绪后发 hello 再要一次（兜底）。
 *   详见 utils/authSync.js。
 *
 * @param {string} path 云端路径，如 '/'（拼到 CLOUD_BASE）
 * @param {string} [iframeTitle] iframe 的 title（无障碍），默认 'Eleball'
 */
export default function CloudFrame({ path = '/', iframeTitle = 'Eleball' }) {
  const [loading, setLoading] = useState(true)
  const iframeRef = useRef(null)

  // 注册 iframe contentWindow，供 authSync 向其 postMessage 推送登录态
  useEffect(() => {
    const win = iframeRef.current?.contentWindow
    if (!win) return
    return registerCloudIframe(win)
  }, [])

  const handleLoad = () => {
    setLoading(false)
    // iframe 文档已加载，推送当前登录态；云端 SPA 监听就绪后还会发 hello 再要一次（兜底）
    broadcastCurrentAuth()
  }

  return (
    <div className="relative h-[calc(100vh-64px)] w-full bg-eleball-bg">
      {loading && (
        <div className="absolute inset-0 flex items-center justify-center">
          <Loader2 className="w-6 h-6 text-eleball-primary animate-spin" />
        </div>
      )}
      <iframe
        ref={iframeRef}
        src={`${CLOUD_BASE}${path}`}
        title={iframeTitle}
        onLoad={handleLoad}
        className="w-full h-full border-0 block"
        sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-popups-to-escape-sandbox allow-top-navigation-by-user-activation"
      />
    </div>
  )
}
