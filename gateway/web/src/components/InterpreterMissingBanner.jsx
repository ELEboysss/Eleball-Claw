import { useState } from 'react'
import { Link } from 'react-router-dom'
import { AlertTriangle, Wrench, DownloadCloud, Loader2, CheckCircle2 } from 'lucide-react'
import { moduleGeneratorApi } from '../api/client'

// 解释器缺失引导横幅（D3 可读报错 + H1 一键自动安装）。
//
// err.data.error_code === 'interpreter_missing' 时渲染。data 形如：
//   { error_code: 'interpreter_missing', interpreter: 'python', hint: '...' }
//
// H1：当缺失的是 python 家族时，提供「自动安装」按钮 -> POST /claw-console/tools/
// install-interpreter，后端下载 python-build-standalone（SHA-256 校验）到
// ~/.eleball-claw/tools/python。成功后提示重新探测；onResolved 回调（可选）可用于
// 父组件自动重探。node/npx 暂不支持托管安装，仅展示「查看安装指南」链接。
//
// 由 ModuleGenerator / MCPInstall 共用，避免安装逻辑重复。
export default function InterpreterMissingBanner({ data, message, onResolved }) {
  const [installing, setInstalling] = useState(false)
  const [result, setResult] = useState(null)
  const [error, setError] = useState(null)

  if (!data || data.error_code !== 'interpreter_missing') return null

  const interpreter = data.interpreter || ''
  // 仅 python 家族支持托管自动安装（node/npx 走指南链接）。
  const canAutoInstall =
    interpreter === 'python' ||
    interpreter === 'python3' ||
    /^python3\.\d+$/.test(interpreter)

  const onInstall = async () => {
    setInstalling(true)
    setError(null)
    setResult(null)
    try {
      const res = await moduleGeneratorApi.installInterpreter({ interpreter: 'python' })
      setResult(res)
      if (onResolved && res?.path) onResolved(res)
    } catch (e) {
      setError(e.message)
    } finally {
      setInstalling(false)
    }
  }

  return (
    <div className="rounded-xl border border-amber-300 bg-amber-50 p-3 flex items-start gap-3">
      <AlertTriangle className="w-5 h-5 text-amber-600 flex-shrink-0 mt-0.5" />
      <div className="flex-1 min-w-0">
        <div className="font-semibold text-amber-800 text-sm">缺少运行时：{interpreter}</div>
        <p className="text-amber-700 mt-1 text-xs leading-relaxed">{data.hint || message}</p>

        <div className="flex flex-wrap items-center gap-3 mt-2">
          {canAutoInstall && !result && (
            <button
              type="button"
              onClick={onInstall}
              disabled={installing}
              className="inline-flex items-center gap-1 text-xs font-medium text-amber-800 hover:underline disabled:opacity-60"
            >
              {installing ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <DownloadCloud className="w-3.5 h-3.5" />}
              {installing ? '正在下载安装…' : '自动安装'}
            </button>
          )}
          {canAutoInstall && result && (
            <span className="inline-flex items-center gap-1 text-xs font-medium text-emerald-700">
              <CheckCircle2 className="w-3.5 h-3.5" />
              已就绪{result.version ? `（${result.version}，${result.source === 'system' ? '系统' : '托管'}）` : ''}，请重新探测
            </span>
          )}
          <Link to="/claw-guide" className="inline-flex items-center gap-1 text-xs font-medium text-amber-800 hover:underline">
            <Wrench className="w-3.5 h-3.5" /> 查看安装指南
          </Link>
        </div>

        {error && (
          <div className="mt-2 text-xs px-2 py-1 rounded-lg bg-red-50 text-red-600 break-all">{error}</div>
        )}
        {result?.reused && result?.source === 'system' && (
          <div className="mt-1 text-[11px] text-amber-700">检测到系统已安装 Python，无需托管下载。</div>
        )}
        {installing && (
          <div className="mt-1 text-[11px] text-amber-700">正在从 astral-sh/python-build-standalone 下载并校验，约 30MB，请稍候…</div>
        )}
      </div>
    </div>
  )
}
