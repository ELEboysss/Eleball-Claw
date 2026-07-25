import { useEffect, useState } from 'react'
import { AlertTriangle, X } from 'lucide-react'
import { systemApi } from '../api/client'
import { getItem, setItem } from '../utils/storage'

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

// 各平台安装指引（链接统一新窗口打开）
function OSGuide() {
  const os = detectOS()
  if (os === 'windows') {
    return (
      <>
        推荐安装{' '}
        <a href="https://www.docker.com/products/docker-desktop/" target="_blank" rel="noreferrer" className="underline font-medium">
          Docker Desktop
        </a>
        ，或命令行执行 <code className="px-1 py-0.5 rounded bg-amber-100 font-mono text-xs">winget install Docker.DockerDesktop</code>
        ；企业用户可选用免费替代{' '}
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
        请安装{' '}
        <a href="https://www.docker.com/products/docker-desktop/" target="_blank" rel="noreferrer" className="underline font-medium">
          Docker Desktop for Mac
        </a>
        。
      </>
    )
  }
  return (
    <>
      执行 <code className="px-1 py-0.5 rounded bg-amber-100 font-mono text-xs">curl -fsSL https://get.docker.com | sh</code>
      （Ubuntu 也可 <code className="px-1 py-0.5 rounded bg-amber-100 font-mono text-xs">apt install docker.io docker-compose-v2</code>）。
    </>
  )
}

// Docker 缺失引导横幅：本地系统状态显示 docker_available=false 时展示；
// 接口不存在/网络错误/已安装 Docker 时静默不渲染。可关闭，关闭状态存 localStorage。
export default function DockerMissingBanner() {
  const [missing, setMissing] = useState(false)

  useEffect(() => {
    if (getItem(DISMISS_KEY)) return
    systemApi
      .status()
      .then((d) => {
        if (d && d.docker_available === false) setMissing(true)
      })
      .catch(() => {})
  }, [])

  if (!missing) return null

  const dismiss = () => {
    setItem(DISMISS_KEY, '1')
    setMissing(false)
  }

  return (
    <div className="mb-6 rounded-xl border border-amber-300 bg-amber-50 px-4 py-3 flex items-start gap-3">
      <AlertTriangle className="w-5 h-5 text-amber-500 shrink-0 mt-0.5" />
      <div className="flex-1 text-sm text-amber-800 space-y-1.5">
        <p className="font-medium">
          未检测到 Docker：内置秘技（联网搜索等）与云端秘技的本地运行需要 Docker。
        </p>
        <p>
          <OSGuide />
        </p>
        <p className="text-xs text-amber-700">安装完成后重启 claw 即可自动上线内置模块。</p>
      </div>
      <button
        onClick={dismiss}
        className="text-amber-500 hover:text-amber-700 shrink-0"
        title="关闭提示"
      >
        <X className="w-4 h-4" />
      </button>
    </div>
  )
}
