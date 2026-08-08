import { useState, useMemo } from 'react'
import { Link } from 'react-router-dom'
import useSEO from '../hooks/useSEO'
import { Loader2, Plus, Trash2, FolderOpen, Play, DownloadCloud, CheckCircle2, Server, Globe, Terminal, Upload, FileText } from 'lucide-react'
import { moduleGeneratorApi } from '../api/client'
import DirectoryPicker from '../components/DirectoryPicker'
import InterpreterMissingBanner from '../components/InterpreterMissingBanner'

// 安装远端 MCP（G3，Smithery 式）：把现成的 stdio/http MCP server 一键装为本地秘技。
// 与「造秘技」的区别：造秘技从 0 写 main.py 生成 user_local 模块；本页指向已存在的远端 MCP
// （如 `npx -y @modelcontextprotocol/server-filesystem /path` 或 http URL），不写脚本，
// 后端建 source=mcp_remote 的 SkillRuntime 并派生 SKU。
// 流程：选 transport -> 填运行配置 -> 探测工具（只读预览）-> 安装（落盘 + 自动上架）。

const TRANSPORTS = [
  { value: 'mcp_stdio', label: 'stdio（本地进程）', icon: Terminal, desc: '启动一个本地 stdio MCP 进程，如 npx server-filesystem' },
  { value: 'mcp_http', label: 'http（远端服务）', icon: Globe, desc: '连接一个远端 HTTP MCP server，填 URL + 请求头' },
]

// 解释器缺失引导横幅（D3 + H1 一键自动安装）已抽到 components/InterpreterMissingBanner，
// 造秘技页与 MCPInstall 页共用，避免安装逻辑重复。

function SectionTitle({ icon: Icon, children, desc }) {
  return (
    <div className="flex items-start gap-2 mb-3">
      <Icon className="w-4 h-4 text-eleball-primary flex-shrink-0 mt-0.5" />
      <div>
        <h2 className="text-sm font-semibold text-eleball-text">{children}</h2>
        {desc && <p className="text-xs text-eleball-text-secondary mt-0.5">{desc}</p>}
      </div>
    </div>
  )
}

// 把 [{name,value}] 折成 {name:value}，跳过空 name
function kvToObject(list) {
  const o = {}
  for (const x of list) {
    if (x.name) o[x.name] = x.value || ''
  }
  return o
}

export default function MCPInstall() {
  // 嵌入 DDIY 工作室（Studio）内容区，页头/SEO 由 Studio 统一负责。

  const [transport, setTransport] = useState('mcp_stdio')
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')

  // stdio 运行配置
  const [command, setCommand] = useState('npx')
  const [argsText, setArgsText] = useState('')
  const [workDir, setWorkDir] = useState('')
  const [env, setEnv] = useState([])
  const [pickerOpen, setPickerOpen] = useState(false)

  // http 配置
  const [endpoint, setEndpoint] = useState('')
  const [headers, setHeaders] = useState([])

  const [probing, setProbing] = useState(false)
  const [probeError, setProbeError] = useState(null)
  const [tools, setTools] = useState(null)

  const [installing, setInstalling] = useState(false)
  const [installError, setInstallError] = useState(null)
  const [result, setResult] = useState(null)

  // M4：批量导入标准 MCP 配置（粘贴 Claude Desktop / Cursor / .mcp.json）
  const [importText, setImportText] = useState('')
  const [importing, setImporting] = useState(false)
  const [importResult, setImportResult] = useState(null)
  const [importError, setImportError] = useState(null)

  const args = useMemo(() => argsText.split(/\s+/).filter(Boolean), [argsText])

  const addEnv = () => setEnv([...env, { name: '', value: '' }])
  const updateEnv = (i, patch) => setEnv(env.map((e, idx) => (idx === i ? { ...e, ...patch } : e)))
  const removeEnv = (i) => setEnv(env.filter((_, idx) => idx !== i))

  const addHeader = () => setHeaders([...headers, { name: '', value: '' }])
  const updateHeader = (i, patch) => setHeaders(headers.map((h, idx) => (idx === i ? { ...h, ...patch } : h)))
  const removeHeader = (i) => setHeaders(headers.filter((_, idx) => idx !== i))

  // 构造探测/安装请求体（共用）
  const buildBody = () => {
    const body = { transport, name, description }
    if (transport === 'mcp_stdio') {
      body.command = command || 'npx'
      body.args = args
      body.env = kvToObject(env)
      body.work_dir = workDir || ''
    } else {
      body.endpoint = endpoint
      body.headers = kvToObject(headers)
    }
    return body
  }

  const onProbe = async () => {
    setProbing(true)
    setProbeError(null)
    setTools(null)
    try {
      const data = await moduleGeneratorApi.probe(buildBody())
      setTools(data?.tools || [])
    } catch (e) {
      setProbeError({ message: e.message, data: e.data })
    } finally {
      setProbing(false)
    }
  }

  const onInstall = async () => {
    if (!name.trim()) {
      setInstallError({ message: '请先填写名称' })
      return
    }
    if (transport === 'mcp_http' && !endpoint.trim()) {
      setInstallError({ message: '请填写 http MCP 地址' })
      return
    }
    setInstalling(true)
    setInstallError(null)
    setResult(null)
    try {
      const data = await moduleGeneratorApi.install(buildBody())
      setResult(data)
      // 安装成功后同步刷新探测到的工具列表（安装返回的 tools 字段）
      if (data?.tools) setTools(data.tools)
    } catch (e) {
      setInstallError({ message: e.message, data: e.data })
    } finally {
      setInstalling(false)
    }
  }

  // M4：批量导入标准 MCP 配置（粘贴 JSON）。客户端先校验 JSON 合法性给出可读错误，再 POST 原对象。
  // 后端逐 server 探测+安装，返回 {results:[{name,transport,ok,result,error_code,message,interpreter,hint}]}。
  const onImport = async () => {
    const text = importText.trim()
    if (!text) {
      setImportError({ message: '请粘贴配置内容' })
      return
    }
    let body
    try {
      body = JSON.parse(text)
    } catch (e) {
      setImportError({ message: '配置不是合法 JSON：' + e.message })
      return
    }
    setImporting(true)
    setImportError(null)
    setImportResult(null)
    try {
      const data = await moduleGeneratorApi.importConfig(body)
      setImportResult(data)
    } catch (e) {
      setImportError({ message: e.message, data: e.data })
    } finally {
      setImporting(false)
    }
  }

  const isStdio = transport === 'mcp_stdio'

  return (
    <div>
      {/* M4：批量导入标准 MCP 配置文件 */}
      <div className="card mb-4">
        <SectionTitle
          icon={FileText}
          desc="粘贴 Claude Desktop / Cursor / .mcp.json 配置，一次导入多个 server（stdio: command/args/env；http: url/headers）。"
        >
          导入配置文件
        </SectionTitle>
        <textarea
          className="input text-xs font-mono w-full"
          rows={8}
          value={importText}
          onChange={(e) => setImportText(e.target.value)}
          placeholder={'{\n  "mcpServers": {\n    "filesystem": {\n      "command": "npx",\n      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"]\n    },\n    "remote": { "url": "https://mcp.example.com/mcp" }\n  }\n}'}
        />
        <div className="flex flex-wrap items-center gap-3 mt-3">
          <button
            type="button"
            onClick={onImport}
            disabled={importing}
            className="btn-primary text-sm px-5 py-2.5 disabled:opacity-50"
          >
            {importing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Upload className="w-4 h-4" />}
            导入
          </button>
          <span className="text-xs text-eleball-text-tertiary">
            逐 server 探测并安装为本地秘技；单个失败不中断其余。
          </span>
        </div>

        {importError && (
          <div className="mt-4 text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{importError.message}</div>
        )}

        {importResult && (
          <div className="mt-4 space-y-2">
            <h3 className="text-xs font-semibold text-eleball-text-secondary">
              导入结果（{importResult.results?.length || 0} 个 server）
            </h3>
            {(importResult.results || []).map((r) => (
              <div
                key={r.name}
                className={`rounded-xl border p-3 ${
                  r.ok ? 'border-emerald-300 bg-emerald-50' : 'border-red-200 bg-red-50'
                }`}
              >
                <div className="flex items-center gap-2">
                  {r.ok ? (
                    <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                  ) : (
                    <span className="text-red-500 font-bold text-sm leading-none">✕</span>
                  )}
                  <span className="font-mono text-xs font-semibold text-eleball-text">{r.name}</span>
                  <span className="text-[11px] text-eleball-text-tertiary">{r.transport}</span>
                  {!r.ok && r.error_code && (
                    <span className="ml-auto text-[11px] font-mono text-red-500">{r.error_code}</span>
                  )}
                </div>
                {r.ok ? (
                  <div className="mt-1.5 pl-6 text-xs text-eleball-text space-y-0.5">
                    <div>运行时：<span className="font-mono break-all">{r.result?.runtime_id}</span></div>
                    <div>派生秘技：<span className="font-mono">{r.result?.sku_count}</span> 个</div>
                  </div>
                ) : (
                  <div className="mt-1.5 pl-6 space-y-2">
                    <div className="text-xs text-red-600">{r.message}</div>
                    {r.error_code === 'interpreter_missing' && (
                      <InterpreterMissingBanner
                        data={{ error_code: 'interpreter_missing', interpreter: r.interpreter, hint: r.hint }}
                        message={r.message}
                        onResolved={onImport}
                      />
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>

      {/* transport 选择 */}
      <div className="card mb-4">
        <SectionTitle icon={Server} desc="stdio 启动本地进程；http 连接远端服务。">
          MCP 类型
        </SectionTitle>
        <div className="grid sm:grid-cols-2 gap-3">
          {TRANSPORTS.map((t) => {
            const Icon = t.icon
            const active = transport === t.value
            return (
              <button
                key={t.value}
                type="button"
                onClick={() => setTransport(t.value)}
                className={`text-left rounded-xl border p-3 transition-colors ${
                  active
                    ? 'border-eleball-primary bg-eleball-primary/5'
                    : 'border-eleball-outline hover:bg-eleball-surface-variant'
                }`}
              >
                <div className="flex items-center gap-2">
                  <Icon className={`w-4 h-4 ${active ? 'text-eleball-primary' : 'text-eleball-text-secondary'}`} />
                  <span className="text-sm font-semibold text-eleball-text">{t.label}</span>
                </div>
                <p className="text-xs text-eleball-text-secondary mt-1">{t.desc}</p>
              </button>
            )
          })}
        </div>
      </div>

      {/* 基本信息 */}
      <div className="card mb-4">
        <SectionTitle icon={Plus} desc="运行时名称与描述，会写进 SkillRuntime 与派生的秘技。">
          基本信息
        </SectionTitle>
        <div className="grid sm:grid-cols-2 gap-3">
          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">名称 *</label>
            <input
              className="input text-sm"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="如：文件系统工具"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">描述</label>
            <input
              className="input text-sm"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="一句话说明这个秘技能做什么"
            />
          </div>
        </div>
      </div>

      {/* 运行配置（按 transport 切换） */}
      <div className="card mb-4">
        <SectionTitle
          icon={isStdio ? Terminal : Globe}
          desc={isStdio ? 'stdio server 的启动命令与参数。' : 'http MCP server 的地址与请求头。'}
        >
          运行配置
        </SectionTitle>

        {isStdio ? (
          <>
            <div className="grid sm:grid-cols-2 gap-3">
              <div>
                <label className="block text-xs font-medium text-eleball-text-secondary mb-1">启动命令</label>
                <input
                  className="input text-sm font-mono"
                  value={command}
                  onChange={(e) => setCommand(e.target.value)}
                  placeholder="npx"
                />
              </div>
              <div>
                <label className="block text-xs font-medium text-eleball-text-secondary mb-1">参数（空格分隔）</label>
                <input
                  className="input text-sm font-mono"
                  value={argsText}
                  onChange={(e) => setArgsText(e.target.value)}
                  placeholder="-y @modelcontextprotocol/server-filesystem /path/to/dir"
                />
              </div>
            </div>
            <div className="mt-3">
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1">工作目录（可选）</label>
              <div className="flex items-center gap-2">
                <input
                  className="input text-sm font-mono"
                  value={workDir}
                  onChange={(e) => setWorkDir(e.target.value)}
                  placeholder="留空则使用默认目录"
                />
                <button
                  type="button"
                  onClick={() => setPickerOpen(true)}
                  className="inline-flex items-center gap-1 px-3 py-2.5 text-xs rounded-2xl border border-eleball-outline hover:bg-eleball-surface-variant whitespace-nowrap"
                >
                  <FolderOpen className="w-4 h-4" /> 选择
                </button>
              </div>
            </div>

            {/* 环境变量 */}
            <div className="mt-4">
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1">环境变量（可选）</label>
              <p className="text-[11px] text-eleball-text-tertiary mb-2">
                敏感值建议用 <code className="font-mono">{'${credentials.KEY}'}</code> 模板，安装后在秘技市场填写实际值；非敏感配置（如 BASE_URL）可直接填字面量。
              </p>
              <div className="space-y-2">
                {env.map((x, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <input
                      className="input text-xs font-mono flex-1"
                      value={x.name}
                      onChange={(e) => updateEnv(i, { name: e.target.value })}
                      placeholder="变量名，如 FIRECRAWL_API_KEY"
                    />
                    <input
                      className="input text-xs font-mono flex-1"
                      value={x.value}
                      onChange={(e) => updateEnv(i, { value: e.target.value })}
                      placeholder={'${credentials.firecrawl_api_key} 或字面量'}
                    />
                    <button
                      type="button"
                      onClick={() => removeEnv(i)}
                      className="p-2 rounded-lg text-eleball-text-tertiary hover:bg-red-50 hover:text-red-500"
                      title="删除"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                ))}
              </div>
              <button
                type="button"
                onClick={addEnv}
                className="mt-2 inline-flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg border border-dashed border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant"
              >
                <Plus className="w-3.5 h-3.5" /> 添加环境变量
              </button>
            </div>
          </>
        ) : (
          <>
            <div>
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1">HTTP MCP 地址 *</label>
              <input
                className="input text-sm font-mono"
                value={endpoint}
                onChange={(e) => setEndpoint(e.target.value)}
                placeholder="https://mcp.example.com/mcp"
              />
            </div>

            <div className="mt-4">
              <label className="block text-xs font-medium text-eleball-text-secondary mb-1">请求头（可选）</label>
              <p className="text-[11px] text-eleball-text-tertiary mb-2">
                鉴权头需填字面量以完成探测（如 <code className="font-mono">Authorization: Bearer xxx</code>），探测与调用时原样发送。
              </p>
              <div className="space-y-2">
                {headers.map((x, i) => (
                  <div key={i} className="flex items-center gap-2">
                    <input
                      className="input text-xs font-mono flex-1"
                      value={x.name}
                      onChange={(e) => updateHeader(i, { name: e.target.value })}
                      placeholder="头名，如 Authorization"
                    />
                    <input
                      className="input text-xs font-mono flex-1"
                      value={x.value}
                      onChange={(e) => updateHeader(i, { value: e.target.value })}
                      placeholder="Bearer xxx"
                    />
                    <button
                      type="button"
                      onClick={() => removeHeader(i)}
                      className="p-2 rounded-lg text-eleball-text-tertiary hover:bg-red-50 hover:text-red-500"
                      title="删除"
                    >
                      <Trash2 className="w-4 h-4" />
                    </button>
                  </div>
                ))}
              </div>
              <button
                type="button"
                onClick={addHeader}
                className="mt-2 inline-flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg border border-dashed border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant"
              >
                <Plus className="w-3.5 h-3.5" /> 添加请求头
              </button>
            </div>
          </>
        )}
      </div>

      {/* 操作区 */}
      <div className="card mb-4">
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={onProbe}
            disabled={probing || installing}
            className="btn-secondary text-sm px-5 py-2.5 disabled:opacity-50"
          >
            {probing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
            探测工具
          </button>
          <button
            type="button"
            onClick={onInstall}
            disabled={installing || probing}
            className="btn-primary text-sm px-5 py-2.5 disabled:opacity-50"
          >
            {installing ? <Loader2 className="w-4 h-4 animate-spin" /> : <DownloadCloud className="w-4 h-4" />}
            安装
          </button>
          <span className="text-xs text-eleball-text-tertiary">探测只读不落盘；安装会创建运行时并自动上架。</span>
        </div>

        {/* 探测错误 */}
        {probeError && (
          <div className="mt-4 space-y-2">
            <InterpreterMissingBanner data={probeError.data} message={probeError.message} onResolved={onProbe} />
            <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{probeError.message}</div>
          </div>
        )}
        {/* 探测到的工具预览 */}
        {tools && !result && (
          <div className="mt-4">
            <h3 className="text-xs font-semibold text-eleball-text-secondary mb-2">探测到 {tools.length} 个工具</h3>
            {tools.length === 0 ? (
              <p className="text-xs text-eleball-text-tertiary">未发现工具，请检查 server 是否实现了 tools/list。</p>
            ) : (
              <div className="space-y-2">
                {tools.map((t) => (
                  <div key={t.name} className="rounded-xl border border-eleball-outline p-2.5">
                    <div className="font-mono text-xs font-semibold text-eleball-text">{t.name}</div>
                    {t.description && <p className="text-xs text-eleball-text-secondary mt-0.5">{t.description}</p>}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        {/* 安装错误 */}
        {installError && (
          <div className="mt-4 space-y-2">
            <InterpreterMissingBanner data={installError.data} message={installError.message} onResolved={onInstall} />
            <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{installError.message}</div>
          </div>
        )}

        {/* 安装结果 */}
        {result && (
          <div className="mt-4 rounded-xl border border-emerald-300 bg-emerald-50 p-4 space-y-3">
            <div className="flex items-center gap-2 text-emerald-700 font-semibold text-sm">
              <CheckCircle2 className="w-5 h-5" /> 远端 MCP 已安装并上架
            </div>
            <div className="text-xs text-eleball-text space-y-1">
              <div>运行时 ID：<span className="font-mono break-all">{result.runtime_id}</span></div>
              <div>类型：<span className="font-mono">{result.transport}</span></div>
              <div>派生秘技数：<span className="font-mono">{result.sku_count}</span></div>
            </div>

            {(result.tools || []).length > 0 && (
              <div className="space-y-2">
                <h3 className="text-xs font-semibold text-eleball-text-secondary">
                  暴露 {result.tools.length} 个工具，已自动上架为秘技
                </h3>
                <div className="space-y-2">
                  {result.tools.map((t) => (
                    <div key={t.name} className="rounded-lg border border-eleball-outline bg-white p-2.5">
                      <div className="font-mono text-xs font-semibold text-eleball-text">{t.name}</div>
                      {t.description && <p className="text-[11px] text-eleball-text-tertiary mt-0.5">{t.description}</p>}
                    </div>
                  ))}
                </div>
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2 pt-1">
              <Link to="/agents" className="btn-primary text-xs px-4 py-2">
                去秘技市场配置凭证并启用
              </Link>
              <span className="text-[11px] text-eleball-text-tertiary">
                安装的秘技默认未启用；若用了 {'${credentials.KEY}'} 模板环境变量/请求头，需在市场填写实际值后开启。
              </span>
            </div>
          </div>
        )}
      </div>

      <DirectoryPicker
        open={pickerOpen}
        onClose={() => setPickerOpen(false)}
        onSelect={(cwd) => setWorkDir(cwd)}
      />
    </div>
  )
}
