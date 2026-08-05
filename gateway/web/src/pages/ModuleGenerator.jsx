import { useState, useMemo, useEffect } from 'react'
import { Link } from 'react-router-dom'
import useSEO from '../hooks/useSEO'
import { Loader2, Plus, Trash2, FolderOpen, Play, PackagePlus, CheckCircle2, Wrench, Sparkles, Terminal } from 'lucide-react'
import { moduleGeneratorApi, modelApi } from '../api/client'
import DirectoryPicker from '../components/DirectoryPicker'
import InterpreterMissingBanner from '../components/InterpreterMissingBanner'

// 造秘技页（阶段 F1）：引导式生成用户 stdio MCP 模块。
// 流程：基本信息 + main.py 草稿（预填 echo 骨架，可编辑）+ 运行配置 + 凭证声明 -> 探测工具 -> 一键生成
// 生成调 /mcp/generate（E3）：写 module.json+main.py -> rescan -> autostart -> DeriveSKUs。
// 探测/生成遇到解释器缺失（D3）展示安装引导。
//
// 三方一致性约定：凭证 key `firecrawl_api_key` -> env 变量 `FIRECRAWL_API_KEY`（=key 大写）
// -> env 模板 `${credentials.firecrawl_api_key}` -> main.py 用 os.environ.get('FIRECRAWL_API_KEY') 读取。
// 本页据此约定自动从凭证推导 env 模板，用户只需保证 main.py 读取同名的环境变量。

// 预填骨架：最小 stdio MCP echo server，用户可直接探测/生成验证链路，再替换为真实逻辑。
const ECHO_SKELETON = `#!/usr/bin/env python3
"""用户模块 stdio MCP 骨架。替换 tools_list/tools_call 为真实逻辑。"""
import json, sys

def make_result(req_id, result): return {"jsonrpc": "2.0", "id": req_id, "result": result}
def make_error(req_id, code, msg): return {"jsonrpc": "2.0", "id": req_id, "error": {"code": code, "message": msg}}

def tools_list():
    return [{"name": "echo", "description": "回显输入",
             "inputSchema": {"type": "object", "properties": {"message": {"type": "string"}}, "required": ["message"]}}]

def tools_call(name, arguments):
    if name == "echo":
        return {"content": [{"type": "text", "text": arguments.get("message", "")}]}
    return {"isError": True, "content": [{"type": "text", "text": "Unknown tool: %s" % name}]}

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line: continue
        try: req = json.loads(line)
        except Exception as e:
            sys.stdout.write(json.dumps(make_error(None, -32700, "Parse error: %s" % e)) + "\\n"); sys.stdout.flush(); continue
        req_id = req.get("id"); method = req.get("method"); params = req.get("params", {}) or {}
        if method == "initialize":
            resp = make_result(req_id, {"protocolVersion": "2024-11-05", "capabilities": {"tools": {}},
                                        "serverInfo": {"name": "user-module", "version": "1.0.0"}})
        elif method == "notifications/initialized": continue
        elif method == "tools/list": resp = make_result(req_id, {"tools": tools_list()})
        elif method == "tools/call": resp = make_result(req_id, tools_call(params.get("name"), params.get("arguments", {})))
        else: resp = make_error(req_id, -32601, "Method not found: %s" % method)
        sys.stdout.write(json.dumps(resp) + "\\n"); sys.stdout.flush()

if __name__ == "__main__":
    main()
`

const CRED_TYPES = [
  { value: 'api_key', label: 'API Key' },
  { value: 'token', label: 'Token' },
  { value: 'cookie', label: 'Cookie' },
]
const CRED_SCOPES = [
  { value: 'module', label: '模块级共享' },
  { value: 'sku', label: '仅本 SKU' },
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

// 据 inputSchema 生成默认参数模板，供测试调用时预填（用户可编辑 JSON）。
function defaultArgsFromSchema(schema) {
  const props = schema?.properties || {}
  const out = {}
  for (const [k, v] of Object.entries(props)) {
    if (v.type === 'string') out[k] = ''
    else if (v.type === 'number' || v.type === 'integer') out[k] = 0
    else if (v.type === 'boolean') out[k] = false
    else out[k] = null
  }
  return out
}

export default function ModuleGenerator() {
  // 嵌入 DDIY 工作室（Studio）内容区，页头/SEO 由 Studio 统一负责。

  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [moduleId, setModuleId] = useState('')
  const [command, setCommand] = useState('python')
  const [argsText, setArgsText] = useState('main.py')
  const [workDir, setWorkDir] = useState('')
  const [mainPy, setMainPy] = useState(ECHO_SKELETON)
  const [creds, setCreds] = useState([])
  const [extraEnv, setExtraEnv] = useState([])
  const [pickerOpen, setPickerOpen] = useState(false)

  const [probing, setProbing] = useState(false)
  const [probeError, setProbeError] = useState(null)
  const [tools, setTools] = useState(null)

  const [generating, setGenerating] = useState(false)
  const [genError, setGenError] = useState(null)
  const [result, setResult] = useState(null)

  // F1 收尾：AI 起草 main.py 草稿（能力描述 + 凭证声明 -> 对话模型生成 stdio MCP 脚本）
  const [capabilityDesc, setCapabilityDesc] = useState('')
  const [models, setModels] = useState([])
  const [modelId, setModelId] = useState('')
  const [drafting, setDrafting] = useState(false)
  const [draftError, setDraftError] = useState(null)

  // F2：测试调用每个工具的状态，按工具名索引 { argsText, calling, result, error }
  const [testState, setTestState] = useState({})

  // 拉取本地可用的对话模型供起草选择（仅 supports_chat，过滤纯视觉模型）
  useEffect(() => {
    let active = true
    modelApi
      .list()
      .then((data) => {
        const list = Array.isArray(data) ? data : data?.items || []
        const chatModels = list.filter((m) => m.supports_chat)
        if (!active) return
        setModels(chatModels)
        if (chatModels.length && !modelId) {
          setModelId(`${chatModels[0].provider}/${chatModels[0].model_name}`)
        }
      })
      .catch(() => {})
    return () => { active = false }
  }, [])

  const args = useMemo(() => argsText.split(/\s+/).filter(Boolean), [argsText])

  // env 模板：凭证自动绑定 ${credentials.KEY}，再合并用户额外的非凭证环境变量。
  const env = useMemo(() => {
    const e = {}
    for (const c of creds) {
      if (c.key) e[c.key.toUpperCase()] = `\${credentials.${c.key}}`
    }
    for (const x of extraEnv) {
      if (x.name) e[x.name] = x.value || ''
    }
    return e
  }, [creds, extraEnv])

  const credentialsMeta = useMemo(() => {
    const m = {}
    for (const c of creds) {
      if (!c.key) continue
      m[c.key] = {
        type: c.type || 'api_key',
        label: c.label || '',
        required: !!c.required,
        scope: c.scope || 'module',
      }
    }
    return m
  }, [creds])

  const addCred = () => setCreds([...creds, { key: '', type: 'api_key', label: '', required: true, scope: 'module' }])
  const updateCred = (i, patch) => setCreds(creds.map((c, idx) => (idx === i ? { ...c, ...patch } : c)))
  const removeCred = (i) => setCreds(creds.filter((_, idx) => idx !== i))

  const addEnv = () => setExtraEnv([...extraEnv, { name: '', value: '' }])
  const updateEnv = (i, patch) => setExtraEnv(extraEnv.map((e, idx) => (idx === i ? { ...e, ...patch } : e)))
  const removeEnv = (i) => setExtraEnv(extraEnv.filter((_, idx) => idx !== i))

  const onProbe = async () => {
    setProbing(true)
    setProbeError(null)
    setTools(null)
    try {
      const data = await moduleGeneratorApi.probe({
        transport: 'mcp_stdio',
        command: command || 'python',
        args: args.length ? args : ['main.py'],
        env,
        work_dir: workDir || '',
      })
      setTools(data?.tools || [])
    } catch (e) {
      setProbeError({ message: e.message, data: e.data })
    } finally {
      setProbing(false)
    }
  }

  const onGenerate = async () => {
    if (!name && !moduleId) {
      setGenError({ message: '请先填写模块名称' })
      return
    }
    setGenerating(true)
    setGenError(null)
    setResult(null)
    try {
      const data = await moduleGeneratorApi.generate({
        command: command || 'python',
        args: args.length ? args : ['main.py'],
        env,
        work_dir: workDir || '',
        credentials_meta: credentialsMeta,
        name,
        description,
        module_id: moduleId || '',
        main_py_content: mainPy,
      })
      setResult(data)
    } catch (e) {
      setGenError({ message: e.message, data: e.data })
    } finally {
      setGenerating(false)
    }
  }

  const onDraft = async () => {
    if (!capabilityDesc.trim()) {
      setDraftError({ message: '请先填写能力描述' })
      return
    }
    if (!modelId) {
      setDraftError({ message: '请选择用于起草的模型（未配置可在「模型」页添加）' })
      return
    }
    setDrafting(true)
    setDraftError(null)
    try {
      const data = await moduleGeneratorApi.draftMain({
        capability_description: capabilityDesc,
        credentials_meta: credentialsMeta,
        command: command || 'python',
        args: args.length ? args : ['main.py'],
        provider: 'eleagent',
        model: modelId,
      })
      if (data?.main_py) setMainPy(data.main_py)
    } catch (e) {
      setDraftError({ message: e.message, data: e.data })
    } finally {
      setDrafting(false)
    }
  }

  const argsTextFor = (tool) =>
    testState[tool.name]?.argsText ?? JSON.stringify(defaultArgsFromSchema(tool.inputSchema), null, 2)

  const setTest = (toolName, patch) =>
    setTestState((s) => ({ ...s, [toolName]: { ...(s[toolName] || {}), ...patch } }))

  const onTestCall = async (tool) => {
    if (!result) return
    let args
    try {
      args = JSON.parse(argsTextFor(tool) || '{}')
    } catch {
      setTest(tool.name, { error: '参数不是合法 JSON，请检查' })
      return
    }
    setTest(tool.name, { calling: true, error: null, result: null })
    try {
      const data = await moduleGeneratorApi.testCall(result.module_id, { tool_name: tool.name, arguments: args })
      setTest(tool.name, { calling: false, result: data, error: null })
    } catch (e) {
      setTest(tool.name, { calling: false, error: e.message })
    }
  }

  return (
    <div>
      {/* 基本信息 */}
      <div className="card mb-4">
        <SectionTitle icon={PackagePlus} desc="模块展示名与描述，会写进 module.json 与派生的秘技。">
          基本信息
        </SectionTitle>
        <div className="grid sm:grid-cols-2 gap-3">
          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">模块名称 *</label>
            <input
              className="input text-sm"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="如：我的翻译工具"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">模块 ID（可选）</label>
            <input
              className="input text-sm font-mono"
              value={moduleId}
              onChange={(e) => setModuleId(e.target.value)}
              placeholder="缺省据名称推导，如 my-translator"
            />
          </div>
        </div>
        <div className="mt-3">
          <label className="block text-xs font-medium text-eleball-text-secondary mb-1">描述</label>
          <textarea
            className="input text-sm h-16 resize-none"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="一句话说明这个秘技能做什么"
          />
        </div>
      </div>

      {/* 脚本编辑 */}
      <div className="card mb-4">
        <SectionTitle icon={Wrench} desc="预填了 echo 骨架可直接探测；替换 tools_list/tools_call 为真实逻辑。凭证通过环境变量读取（见下方凭证声明）。">
          main.py 脚本
        </SectionTitle>

        {/* AI 起草（F1 收尾）：能力描述 + 模型选择 -> 生成 main.py 草稿填入下方编辑器 */}
        <div className="rounded-xl border border-eleball-outline p-3 mb-3 bg-eleball-surface-variant/40">
          <label className="block text-xs font-medium text-eleball-text-secondary mb-1">能力描述</label>
          <textarea
            className="input text-sm h-20 resize-none mb-2"
            value={capabilityDesc}
            onChange={(e) => setCapabilityDesc(e.target.value)}
            placeholder="用自然语言描述这个秘技应做什么、暴露哪些工具。如：提供 translate 工具，把中文翻译成英文，调用某翻译 API"
          />
          <div className="flex flex-wrap items-center gap-2">
            <select
              className="input text-xs font-mono max-w-[260px]"
              value={modelId}
              onChange={(e) => setModelId(e.target.value)}
              disabled={drafting}
            >
              {models.length === 0 && <option value="">未配置对话模型</option>}
              {models.map((m) => (
                <option key={`${m.provider}/${m.model_name}`} value={`${m.provider}/${m.model_name}`}>
                  {m.display_name || `${m.provider}/${m.model_name}`}
                </option>
              ))}
            </select>
            <button
              type="button"
              onClick={onDraft}
              disabled={drafting || !modelId || !capabilityDesc.trim()}
              className="btn-primary text-xs px-4 py-2 disabled:opacity-50"
            >
              {drafting ? <Loader2 className="w-3.5 h-3.5 animate-spin" /> : <Sparkles className="w-3.5 h-3.5" />}
              AI 起草
            </button>
            <span className="text-[11px] text-eleball-text-tertiary">据能力描述用所选模型生成草稿，可在下方编辑后再探测/生成。</span>
          </div>
          {draftError && (
            <div className="mt-2 text-xs px-2 py-1 rounded-lg bg-red-50 text-red-600">{draftError.message}</div>
          )}
        </div>

        <textarea
          className="input text-xs font-mono h-72 resize-y leading-relaxed"
          value={mainPy}
          onChange={(e) => setMainPy(e.target.value)}
          spellCheck={false}
        />
      </div>

      {/* 运行配置 */}
      <div className="card mb-4">
        <SectionTitle icon={Play} desc="启动命令与参数。work_dir 是进程运行目录（可选）；若其中已有同名脚本，可在上方留空骨架。">
          运行配置
        </SectionTitle>
        <div className="grid sm:grid-cols-2 gap-3">
          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">启动命令</label>
            <input
              className="input text-sm font-mono"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="python"
            />
          </div>
          <div>
            <label className="block text-xs font-medium text-eleball-text-secondary mb-1">参数（空格分隔）</label>
            <input
              className="input text-sm font-mono"
              value={argsText}
              onChange={(e) => setArgsText(e.target.value)}
              placeholder="main.py"
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
              placeholder="留空则使用生成目录"
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
      </div>

      {/* 凭证声明 */}
      <div className="card mb-4">
        <SectionTitle icon={Plus} desc="声明秘技需要的 API Key/Token。每项会自动绑定环境变量 KEY 大写名（脚本用 os.environ.get 读取），并在生成后由秘技市场引导用户填写。">
          凭证声明
        </SectionTitle>
        {creds.length === 0 && (
          <p className="text-xs text-eleball-text-tertiary py-2">暂无凭证。若脚本无需 API Key 可跳过。</p>
        )}
        <div className="space-y-2">
          {creds.map((c, i) => (
            <div key={i} className="rounded-xl border border-eleball-outline p-3 space-y-2">
              <div className="flex items-center gap-2">
                <input
                  className="input text-xs font-mono flex-1"
                  value={c.key}
                  onChange={(e) => updateCred(i, { key: e.target.value })}
                  placeholder="凭证 key，如 firecrawl_api_key"
                />
                <button
                  type="button"
                  onClick={() => removeCred(i)}
                  className="p-2 rounded-lg text-eleball-text-tertiary hover:bg-red-50 hover:text-red-500"
                  title="删除"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
              <div className="grid grid-cols-2 sm:grid-cols-4 gap-2">
                <input
                  className="input text-xs"
                  value={c.label}
                  onChange={(e) => updateCred(i, { label: e.target.value })}
                  placeholder="展示标签"
                />
                <select
                  className="input text-xs"
                  value={c.type}
                  onChange={(e) => updateCred(i, { type: e.target.value })}
                >
                  {CRED_TYPES.map((t) => (
                    <option key={t.value} value={t.value}>{t.label}</option>
                  ))}
                </select>
                <select
                  className="input text-xs"
                  value={c.scope}
                  onChange={(e) => updateCred(i, { scope: e.target.value })}
                >
                  {CRED_SCOPES.map((s) => (
                    <option key={s.value} value={s.value}>{s.label}</option>
                  ))}
                </select>
                <label className="inline-flex items-center gap-1.5 text-xs text-eleball-text-secondary px-2">
                  <input
                    type="checkbox"
                    checked={c.required}
                    onChange={(e) => updateCred(i, { required: e.target.checked })}
                    className="rounded"
                  />
                  必填
                </label>
              </div>
              {c.key && (
                <p className="text-[11px] text-eleball-text-tertiary font-mono">
                  环境变量：{c.key.toUpperCase()} = {'${credentials.' + c.key + '}'} · 脚本读取 os.environ.get('{c.key.toUpperCase()}')
                </p>
              )}
            </div>
          ))}
        </div>
        <button
          type="button"
          onClick={addCred}
          className="mt-3 inline-flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg border border-dashed border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant"
        >
          <Plus className="w-3.5 h-3.5" /> 添加凭证
        </button>

        {/* 额外环境变量 */}
        {extraEnv.length > 0 && (
          <div className="mt-4 space-y-2">
            <label className="block text-xs font-medium text-eleball-text-secondary">额外环境变量</label>
            {extraEnv.map((x, i) => (
              <div key={i} className="flex items-center gap-2">
                <input
                  className="input text-xs font-mono flex-1"
                  value={x.name}
                  onChange={(e) => updateEnv(i, { name: e.target.value })}
                  placeholder="变量名，如 BASE_URL"
                />
                <input
                  className="input text-xs font-mono flex-1"
                  value={x.value}
                  onChange={(e) => updateEnv(i, { value: e.target.value })}
                  placeholder="值"
                />
                <button
                  type="button"
                  onClick={() => removeEnv(i)}
                  className="p-2 rounded-lg text-eleball-text-tertiary hover:bg-red-50 hover:text-red-500"
                >
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            ))}
          </div>
        )}
        <button
          type="button"
          onClick={addEnv}
          className="mt-2 inline-flex items-center gap-1 px-3 py-1.5 text-xs rounded-lg border border-dashed border-eleball-outline text-eleball-text-secondary hover:bg-eleball-surface-variant"
        >
          <Plus className="w-3.5 h-3.5" /> 添加环境变量
        </button>
      </div>

      {/* 操作区 */}
      <div className="card mb-4">
        <div className="flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={onProbe}
            disabled={probing || generating}
            className="btn-secondary text-sm px-5 py-2.5 disabled:opacity-50"
          >
            {probing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Play className="w-4 h-4" />}
            探测工具
          </button>
          <button
            type="button"
            onClick={onGenerate}
            disabled={generating || probing}
            className="btn-primary text-sm px-5 py-2.5 disabled:opacity-50"
          >
            {generating ? <Loader2 className="w-4 h-4 animate-spin" /> : <PackagePlus className="w-4 h-4" />}
            生成模块
          </button>
          <span className="text-xs text-eleball-text-tertiary">探测只读不落盘；生成会写入并自动上架。</span>
        </div>

        {/* 探测结果 */}
        {probeError && (
          <div className="mt-4 space-y-2">
            <InterpreterMissingBanner data={probeError.data} message={probeError.message} onResolved={onProbe} />
            <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{probeError.message}</div>
          </div>
        )}
        {tools && (
          <div className="mt-4">
            <h3 className="text-xs font-semibold text-eleball-text-secondary mb-2">探测到 {tools.length} 个工具</h3>
            {tools.length === 0 ? (
              <p className="text-xs text-eleball-text-tertiary">未发现工具，请检查脚本是否实现了 tools/list。</p>
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

        {/* 生成结果 */}
        {genError && (
          <div className="mt-4 space-y-2">
            <InterpreterMissingBanner data={genError.data} message={genError.message} />
            <div className="text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{genError.message}</div>
          </div>
        )}
        {result && (
          <div className="mt-4 rounded-xl border border-emerald-300 bg-emerald-50 p-4 space-y-3">
            <div className="flex items-center gap-2 text-emerald-700 font-semibold text-sm">
              <CheckCircle2 className="w-5 h-5" /> 模块已生成并上架
            </div>
            <div className="text-xs text-eleball-text space-y-1">
              <div>模块 ID：<span className="font-mono">{result.module_id}</span></div>
              <div>目录：<span className="font-mono break-all">{result.module_dir}</span></div>
            </div>

            {/* 派生工具/SKU 列表 + 测试调用（F2） */}
            {(result.tools || []).length > 0 && (
              <div className="space-y-2">
                <h3 className="text-xs font-semibold text-eleball-text-secondary">
                  派生 {result.tools.length} 个工具，已自动上架为秘技（SKU ID：<span className="font-mono">{result.module_id}</span>-&lt;工具名&gt;）
                </h3>
                {result.tools.map((t) => {
                  const st = testState[t.name] || {}
                  return (
                    <div key={t.name} className="rounded-lg border border-eleball-outline bg-white p-2.5 space-y-2">
                      <div className="flex items-center justify-between gap-2">
                        <div className="min-w-0">
                          <div className="font-mono text-xs font-semibold text-eleball-text">{t.name}</div>
                          {t.description && <p className="text-[11px] text-eleball-text-tertiary truncate">{t.description}</p>}
                        </div>
                        <button
                          type="button"
                          onClick={() => onTestCall(t)}
                          disabled={st.calling}
                          className="inline-flex items-center gap-1 px-2.5 py-1 text-[11px] rounded-lg border border-eleball-outline hover:bg-eleball-surface-variant disabled:opacity-50 whitespace-nowrap"
                        >
                          {st.calling ? <Loader2 className="w-3 h-3 animate-spin" /> : <Terminal className="w-3 h-3" />}
                          测试调用
                        </button>
                      </div>
                      <textarea
                        className="input text-[11px] font-mono h-16 resize-y w-full"
                        value={argsTextFor(t)}
                        onChange={(e) => setTest(t.name, { argsText: e.target.value })}
                        spellCheck={false}
                        placeholder="工具参数 JSON"
                      />
                      {st.error && <div className="text-[11px] px-2 py-1 rounded bg-red-50 text-red-600 break-all">{st.error}</div>}
                      {st.result != null && (
                        <pre className="text-[11px] font-mono whitespace-pre-wrap break-all bg-eleball-surface-variant rounded p-2 max-h-48 overflow-auto">
                          {JSON.stringify(st.result, null, 2)}
                        </pre>
                      )}
                    </div>
                  )
                })}
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2 pt-1">
              <Link to="/agents" className="btn-primary text-xs px-4 py-2">
                去秘技市场配置凭证并启用
              </Link>
              <span className="text-[11px] text-eleball-text-tertiary">
                生成的秘技默认未启用；需在市场填写凭证（若有）后开启。修改脚本后重新生成会自动重启模块加载新代码。
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
