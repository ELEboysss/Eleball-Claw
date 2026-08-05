import { CLOUD_BASE } from '../api/client'
import { getItem, setItem, getJSON } from './storage'

// claw 与内嵌云端内容 iframe（eleball.cn，如充值页）之间的登录态双向同步。
//
// 背景：claw 与 eleball.cn 不同源，localStorage 各自隔离；但 claw 登录拿到的 JWT 本就是
// 有效的云端 token（两端共享 JWT secret + storage 键名格式完全一致：eleball_token /
// eleball_refresh_token / eleball_user）。所以只需把 token 跨源搬运，登录态即可统一。
//
// 本文件是「父窗口（claw）」角色：
// - broadcastAuthToCloud：把 claw 当前 token 推给云端 iframe（targetOrigin 锁云端域名）。
// - startCloudAuthListener：收云端 iframe 回传的登录态（iframe 内登录后同步回 claw），并响应 hello。
// - 同步由 login/logout/refresh 事件驱动，收到回传只写本地不 rebroadcast，避免死循环。
// 云端侧（gateway/web）的对应实现见其 utils/authSync.js（iframe 角色）。

const MSG_TYPE = 'eleball:auth-sync' // { type, v, ts, token, refresh_token, user }
const MSG_HELLO = 'eleball:auth-sync-hello' // iframe 监听就绪后向父请求当前登录态
const TS_KEY = 'auth_ts' // 上次应用的同步时间戳（防更旧消息覆盖更新）

// 已注册的云端 iframe contentWindow 集合
const iframeWindows = new Set()

export function registerCloudIframe(win) {
  if (win) iframeWindows.add(win)
  return () => { iframeWindows.delete(win) }
}

// 把 claw 当前登录态广播给所有已注册的云端 iframe
export function broadcastAuthToCloud(payload) {
  if (iframeWindows.size === 0) return
  const msg = { type: MSG_TYPE, v: 1, ts: Date.now(), ...payload }
  iframeWindows.forEach((win) => {
    try {
      win.postMessage(msg, CLOUD_BASE)
    } catch {
      /* iframe 已卸载则忽略 */
    }
  })
}

// 读 claw storage 当前登录态并广播（iframe 加载/hello 时推送）
export function broadcastCurrentAuth() {
  broadcastAuthToCloud({
    token: getItem('token'),
    refresh_token: getItem('refresh_token'),
    user: getJSON('user'),
  })
}

// 监听云端 iframe 回传的登录态。onSync(payload) 在收到更长的同步消息时调用。
export function startCloudAuthListener(onSync) {
  function handler(event) {
    if (event.origin !== CLOUD_BASE) return // 只接受云端域名来源
    const data = event.data
    if (!data || typeof data !== 'object') return
    if (data.type === MSG_HELLO) {
      broadcastCurrentAuth() // iframe 请求当前态
      return
    }
    if (data.type === MSG_TYPE) {
      const curTs = Number(getItem(TS_KEY) || 0)
      if (data.ts && data.ts < curTs) return // 更旧，忽略防覆盖
      setItem(TS_KEY, String(data.ts || Date.now()))
      onSync(data)
    }
  }
  window.addEventListener('message', handler)
  return () => window.removeEventListener('message', handler)
}
