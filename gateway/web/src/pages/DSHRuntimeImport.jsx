import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { Cable, Loader2, CheckCircle2, AlertTriangle, KeyRound, Play } from 'lucide-react'
import { moduleGeneratorApi } from '../api/client'

// DSH 运行时导入（T5）：把 DSH 工具插件（@deepseek-ai/dsh-tool-* npm 包）经 dsh-mcp-bridge
// 挂载为本地 mcp_stdio 秘技运行时——与「MCP 安装」同一产物（SkillRuntime + 派生 SKU），
// 但无需用户手写 server：桥接器自动补齐能力提供者、转换工具 schema。
// 与「DSH 插件」tab（F4，扫描包内 SKILL.md/MCP 配置落盘）互补：那里导入静态资产，这里挂载实时工具。

// 能力标签文案（T6）：导入前明示工具触达面
const TAG_LABELS = {
  filesystem: '读写本地文件',
  shell: '执行 shell 命令',
  subprocess: 'spawn 子进程',
  network: '出站网络请求'
}

export default function DSHRuntimeImport() {
  const [status, setStatus] = useState(null)
  const [statusError, setStatusError] = useState(null)
  const [loading, setLoading] = useState(true)
  const [selected, setSelected] = useState('')
  const [customPkg, setCustomPkg] = useState('')
  const [dshHome, setDshHome] = useState('')
  const [envValues, setEnvValues] = useState({})
  const [importing, setImporting] = useState(false)
  const [importError, setImportError] = useState(null)
  const [result, setResult] = useState(null)

  useEffect(() => {
    let cancelled = false
    moduleGeneratorApi.dshBridgeStatus()
      .then((data) => {
        if (cancelled) return
        setStatus(data)
        setDshHome(data?.dsh_home || '')
      })
      .catch((e) => { if (!cancelled) setStatusError({ message: e.message }) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [])

  const plugins = status?.plugins || []
  const selectedMeta = useMemo(() => plugins.find((p) => p.package === selected) || null, [plugins, selected])
  // 实际导入的包名：选中预设优先，否则自由输入
  const targetPackage = selected || customPkg.trim()

  const onSelect = (pkg) => {
    setSelected(pkg)
    setCustomPkg('')
    setResult(null)
    setImportError(null)
    // 选中预设时预填空值凭据行（T5 凭据映射：值由用户填写，随运行时 env 注入桥接进程）
    const meta = plugins.find((p) => p.package === pkg)
    const rows = {}
    for (const e of meta?.env || []) rows[e.name] = ''
    setEnvValues(rows)
  }

  const onImport = async () => {
    if (!targetPackage) {
      setImportError({ message: '请选择或输入一个 DSH 插件包名' })
      return
    }
    setImporting(true)
    setImportError(null)
    setResult(null)
    try {
      const env = {}
      for (const [k, v] of Object.entries(envValues)) {
        if (v !== '') env[k] = v
      }
      const data = await moduleGeneratorApi.dshImportRuntime({
        package: targetPackage,
        ...(dshHome.trim() ? { dsh_home: dshHome.trim() } : {}),
        ...(Object.keys(env).length > 0 ? { env } : {})
      })
      setResult(data)
    } catch (e) {
      setImportError({ message: e.message })
    } finally {
      setImporting(false)
    }
  }

  if (loading) {
    return <div className="card flex items-center gap-2 text-sm text-eleball-text-secondary"><Loader2 className="w-4 h-4 animate-spin" /> 正在探测本地 DSH 桥接环境…</div>
  }

  return (
    <div>
      <div className="card mb-4">
        <div className="flex items-start gap-2 mb-3">
          <Cable className="w-4 h-4 text-eleball-primary flex-shrink-0 mt-0.5" />
          <div>
            <h2 className="text-sm font-semibold text-eleball-text">导入 DSH 运行时</h2>
            <p className="text-xs text-eleball-text-secondary mt-0.5">
              把 DSH 工具插件（@deepseek-ai/dsh-tool-*）经 dsh-mcp-bridge 挂载为可实时调用的秘技运行时，工具自动派生 SKU 上架。
              插件来自本机已安装的 DSH 包树（无需联网下载）。
            </p>
          </div>
        </div>

        {statusError && (
          <div className="rounded-lg border border-eleball-outline p-3 text-xs text-eleball-text-secondary mb-3">
            桥接环境探测失败：{statusError.message}
          </div>
        )}
        {status && !status.available && (
          <div className="rounded-lg border border-amber-500/40 bg-amber-500/5 p-3 text-xs text-amber-700 dark:text-amber-400 mb-3 flex items-start gap-2">
            <AlertTriangle className="w-4 h-4 flex-shrink-0 mt-0.5" />
            <div>
              <div className="font-semibold mb-0.5">桥接环境未就绪</div>
              <div>{status.hint || '请确认已安装 Node.js、dsh-mcp-bridge 与 DSH 包树（npm install @deepseek-ai/dsh）。'}</div>
            </div>
          </div>
        )}

        {/* 插件选择：预设卡片 + 自由输入 */}
        <div className="space-y-2 mb-3">
          {plugins.map((p) => {
            const active = selected === p.package
            return (
              <button
                key={p.package}
                type="button"
                onClick={() => onSelect(active ? '' : p.package)}
                className={`w-full text-left rounded-xl border p-3 transition-colors ${
                  active ? 'border-eleball-primary bg-eleball-primary/5' : 'border-eleball-outline hover:bg-eleball-surface-variant'
                }`}
              >
                <div className="flex items-center justify-between gap-2">
                  <div className={`text-sm font-semibold ${active ? 'text-eleball-primary' : 'text-eleball-text'}`}>{p.label}</div>
                  <div className="flex gap-1 flex-shrink-0">
                    {(p.tags || []).map((t) => (
                      <span key={t} className="text-[10px] px-1.5 py-0.5 rounded bg-eleball-surface-variant text-eleball-text-secondary">{TAG_LABELS[t] || t}</span>
                    ))}
                  </div>
                </div>
                <div className="text-xs text-eleball-text-secondary mt-0.5">{p.description}</div>
                <div className="text-[11px] text-eleball-text-secondary/70 font-mono mt-1">{p.package} → {(p.tools || []).join(', ')}</div>
              </button>
            )
          })}
        </div>

        <input
          className="input text-sm w-full font-mono mb-3"
          value={customPkg}
          onChange={(e) => { setCustomPkg(e.target.value); setSelected('') }}
          placeholder="或输入其他 DSH 插件包名（其能力依赖需已被桥接预设覆盖）"
        />

        {/* dsh-home 目录 */}
        <label className="block text-xs font-semibold text-eleball-text-secondary mb-1">DSH 安装目录（dsh-home）</label>
        <input
          className="input text-sm w-full font-mono mb-3"
          value={dshHome}
          onChange={(e) => setDshHome(e.target.value)}
          placeholder="含 node_modules/@deepseek-ai/* 的目录"
          list="dsh-home-candidates"
        />
        <datalist id="dsh-home-candidates">
          {(status?.dsh_home_candidates || []).map((c) => <option key={c} value={c} />)}
        </datalist>

        {/* 凭据行（按所选插件预填） */}
        {selectedMeta && selectedMeta.env.length > 0 && (
          <div className="rounded-lg border border-eleball-outline p-3 mb-3">
            <div className="flex items-center gap-1.5 text-xs font-semibold text-eleball-text mb-2">
              <KeyRound className="w-3.5 h-3.5" /> 凭据（随运行时环境变量注入桥接进程）
            </div>
            {selectedMeta.env.map((e) => (
              <div key={e.name} className="mb-2 last:mb-0">
                <label className="block text-[11px] font-mono text-eleball-text-secondary mb-0.5">
                  {e.name}{e.required && <span className="text-red-500"> *</span>}
                </label>
                <input
                  type="password"
                  className="input text-sm w-full font-mono"
                  value={envValues[e.name] || ''}
                  onChange={(ev) => setEnvValues((prev) => ({ ...prev, [e.name]: ev.target.value }))}
                  placeholder={e.hint}
                />
              </div>
            ))}
            <p className="text-[11px] text-eleball-text-secondary/80 mt-1.5">留空则回退到桥接进程的系统环境变量。</p>
          </div>
        )}

        <button
          type="button"
          onClick={onImport}
          disabled={importing || !targetPackage || (status && !status.available)}
          className="btn-primary text-sm px-4 py-2 disabled:opacity-50 flex items-center gap-1.5"
        >
          {importing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
          {importing ? '正在探测并安装…' : '导入并安装'}
        </button>

        {importError && (
          <div className="rounded-lg border border-red-500/40 bg-red-500/5 p-3 text-xs text-red-600 dark:text-red-400 mt-3">
            {importError.message}
          </div>
        )}
      </div>

      {result && (
        <div className="card">
          <div className="flex items-center gap-2 text-sm font-semibold text-eleball-text mb-2">
            <CheckCircle2 className="w-4 h-4 text-green-500" /> 安装成功
          </div>
          <div className="text-xs text-eleball-text-secondary space-y-1">
            <div>运行时 ID：<span className="font-mono">{result.runtime_id}</span></div>
            <div>工具 {result.tools?.length ?? 0} 个，已派生 SKU {result.sku_count ?? 0} 个（秘技集市可见）</div>
            <div className="font-mono text-[11px] mt-1">{(result.tools || []).map((t) => t.name).join(', ')}</div>
          </div>
          <p className="text-xs text-eleball-text-secondary mt-3">
            现在可以在 <Link className="text-eleball-primary hover:underline" to="/chat">对话页</Link> 让 Agent 调用这些工具，或在秘技集市查看派生的 SKU。
          </p>
        </div>
      )}
    </div>
  )
}
