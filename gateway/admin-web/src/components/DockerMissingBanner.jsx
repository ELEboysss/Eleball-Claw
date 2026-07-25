import { useEffect, useState } from 'react'
import { systemApi } from '../api/client'

// localStorage 关闭标记：用户手动关闭后本次安装不再打扰
const DISMISS_KEY = 'docker_missing_banner_dismissed'

// 简单判定操作系统，给出对应的 Docker 安装指引
function detectOS() {
  const p = (navigator.platform || '').toLowerCase()
  const ua = (navigator.userAgent || '').toLowerCase()
  if (p.includes('win') || ua.includes('windows')) return 'windows'
  if (p.includes('mac') || ua.includes('mac os')) return 'mac'
  return 'linux'
}

const codeCls = 'px-1 py-0.5 rounded bg-amber-100 font-mono text-xs'

// 各平台安装指引（链接统一新窗口打开）
function OSGuide() {
  const os = detectOS()
  if (os === 'windows') {
    return (
      <>
        Windows：安装{' '}
        <a href="https://www.docker.com/products/docker-desktop/" target="_blank" rel="noreferrer" className="underline font-medium">
          Docker Desktop
        </a>
        {' '}或 <code className={codeCls}>winget install Docker.DockerDesktop</code>
        ；企业环境可用免费替代{' '}
        <a href="https://rancherdesktop.io" target="_blank" rel="noreferrer" className="underline font-medium">
          Rancher Desktop
        </a>
        。
      </>
    )
  }
  if (os === 'mac') {
    return (
      <>
        macOS：安装{' '}
        <a href="https://www.docker.com/products/docker-desktop/" target="_blank" rel="noreferrer" className="underline font-medium">
          Docker Desktop for Mac
        </a>
        。
      </>
    )
  }
  return (
    <>
      Linux：<code className={codeCls}>curl -fsSL https://get.docker.com | sh</code>
      （Ubuntu 也可 <code className={codeCls}>apt install docker.io docker-compose-v2</code>）。
    </>
  )
}

// Docker 缺失引导横幅：系统状态显示 docker_available=false 时展示；
// 接口不存在/网络错误/已安装 Docker 时静默不渲染。可关闭，关闭状态存 localStorage。
export default function DockerMissingBanner() {
  const [status, setStatus] = useState(null)

  useEffect(() => {
    if (localStorage.getItem(DISMISS_KEY)) return
    systemApi
      .status()
      .then((d) => {
        if (d && d.docker_available === false) setStatus(d)
      })
      .catch(() => {})
  }, [])

  if (!status) return null

  const dismiss = () => {
    localStorage.setItem(DISMISS_KEY, '1')
    setStatus(null)
  }

  return (
    <div className="rounded-xl border border-amber-300 bg-amber-50 px-4 py-3 flex items-start gap-3">
      <span className="text-amber-500 text-lg leading-6 shrink-0">⚠</span>
      <div className="flex-1 text-sm text-amber-800 space-y-1.5">
        <p className="font-medium">
          未检测到 Docker{status.compose_available === false ? ' / Docker Compose' : ''}
          ：内置模块无法随 serve 自动拉起（modules.auto_start={String(status.modules_auto_start ?? true)}），云端第三方秘技的镜像安装/运行也不可用。
        </p>
        <p>
          <OSGuide />
        </p>
        <p className="text-xs text-amber-700">
          安装完成后重启 claw 即可自动上线内置模块；也可在 Docker 就绪后手动执行{' '}
          <code className={codeCls}>claw module up &lt;module-id&gt;</code> 拉起单个模块。
        </p>
      </div>
      <button
        onClick={dismiss}
        className="text-amber-500 hover:text-amber-700 shrink-0 text-lg leading-5"
        title="关闭提示"
      >
        &times;
      </button>
    </div>
  )
}
