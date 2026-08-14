import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Package, Loader2, Search, CheckCircle2, DownloadCloud, Puzzle } from 'lucide-react'
import { moduleGeneratorApi } from '../api/client'

// DSH 插件导入（F4）：DSH 插件即 npm 包（@deepseek-ai/dsh-* / dsh-plugin-* / 任意兼容包）。
// 输入包名 -> 后端从 npm registry 拉 tarball 扫描 Anthropic 标准 SKILL.md 与 mcpServers 配置
// -> 预览勾选 -> 导入为本地秘技（SKILL.md 全字段保留）/ MCP server（探测安装）。

export default function DSHPlugin() {
  const [pkg, setPkg] = useState('')
  const [previewing, setPreviewing] = useState(false)
  const [previewError, setPreviewError] = useState(null)
  const [preview, setPreview] = useState(null)
  // 勾选项：秘技按包内路径；MCP 按名称（默认全选秘技、不选 MCP）
  const [selectedSkills, setSelectedSkills] = useState(() => new Set())
  const [selectedMcps, setSelectedMcps] = useState(() => new Set())
  const [importing, setImporting] = useState(false)
  const [importError, setImportError] = useState(null)
  const [result, setResult] = useState(null)

  const onPreview = async () => {
    if (!pkg.trim()) {
      setPreviewError({ message: '请输入 npm 包名' })
      return
    }
    setPreviewing(true)
    setPreviewError(null)
    setPreview(null)
    setResult(null)
    setImportError(null)
    try {
      const data = await moduleGeneratorApi.dshPluginPreview(pkg.trim())
      setPreview(data)
      setSelectedSkills(new Set((data?.skills || []).map((s) => s.path)))
      setSelectedMcps(new Set())
    } catch (e) {
      setPreviewError({ message: e.message })
    } finally {
      setPreviewing(false)
    }
  }

  const toggleSet = (setter, key) => {
    setter((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const onImport = async () => {
    if (!preview) return
    setImporting(true)
    setImportError(null)
    setResult(null)
    try {
      const data = await moduleGeneratorApi.dshPluginImport({
        package: preview.package + '@' + preview.version,
        skills: [...selectedSkills],
        mcp_servers: [...selectedMcps]
      })
      setResult(data)
    } catch (e) {
      // 部分失败时后端回 2002 + data（含成功项与 skipped 原因）
      setImportError({ message: e.message })
      if (e.data) setResult(e.data)
    } finally {
      setImporting(false)
    }
  }

  return (
    <div>
      <div className="card mb-4">
        <div className="flex items-start gap-2 mb-3">
          <Puzzle className="w-4 h-4 text-eleball-primary flex-shrink-0 mt-0.5" />
          <div>
            <h2 className="text-sm font-semibold text-eleball-text">导入 DSH 插件（npm 包）</h2>
            <p className="text-xs text-eleball-text-secondary mt-0.5">
              DSH 插件是标准 npm 包。输入包名后自动扫描其中的 SKILL.md（Anthropic 开放标准）与 MCP server 配置，勾选即可导入为本地秘技。
            </p>
          </div>
        </div>

        <div className="flex gap-2">
          <input
            className="input text-sm flex-1 font-mono"
            value={pkg}
            onChange={(e) => setPkg(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && !previewing && onPreview()}
            placeholder="如 dsh-plugin-xxx 或 @scope/name@1.0.0"
          />
          <button
            type="button"
            onClick={onPreview}
            disabled={previewing}
            className="btn-primary text-sm px-4 py-2 disabled:opacity-50"
          >
            {previewing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Search className="w-4 h-4" />}
            预览
          </button>
        </div>
        <p className="mt-2 text-xs text-eleball-text-tertiary">
          包名来源：<a href="https://www.npmjs.com/search?q=dsh-plugin" target="_blank" rel="noreferrer" className="text-eleball-primary hover:underline">npmjs.com</a> 搜索 dsh-plugin / @deepseek-ai；插件需内含 SKILL.md 或 mcpServers 配置才可导入。
        </p>

        {previewError && (
          <div className="mt-4 text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{previewError.message}</div>
        )}
      </div>

      {preview && (
        <div className="card mb-4">
          <div className="flex items-start gap-2 mb-3">
            <Package className="w-4 h-4 text-eleball-primary flex-shrink-0 mt-0.5" />
            <div className="min-w-0">
              <h2 className="text-sm font-semibold text-eleball-text font-mono truncate">
                {preview.package}@{preview.version}
              </h2>
              {preview.description && (
                <p className="text-xs text-eleball-text-secondary mt-0.5">{preview.description}</p>
              )}
            </div>
          </div>

          {preview.skills?.length > 0 && (
            <div className="mb-3">
              <div className="text-xs font-medium text-eleball-text-secondary mb-1.5">秘技（SKILL.md）</div>
              <div className="space-y-1.5">
                {preview.skills.map((s) => (
                  <label key={s.path} className="flex items-start gap-2 rounded-lg border border-eleball-outline-variant p-2.5 cursor-pointer hover:bg-eleball-surface-variant">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={selectedSkills.has(s.path)}
                      onChange={() => toggleSet(setSelectedSkills, s.path)}
                    />
                    <span className="min-w-0">
                      <span className="text-sm font-medium text-eleball-text">{s.name}</span>
                      <span className="ml-2 text-xs text-eleball-text-tertiary font-mono">{s.slug}</span>
                      <span className="block text-xs text-eleball-text-secondary mt-0.5">{s.description}</span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
          )}

          {preview.mcp_servers?.length > 0 && (
            <div className="mb-3">
              <div className="text-xs font-medium text-eleball-text-secondary mb-1.5">
                MCP server（导入时逐个探测安装，可能耗时较长）
              </div>
              <div className="space-y-1.5">
                {preview.mcp_servers.map((m) => (
                  <label key={m.name} className="flex items-start gap-2 rounded-lg border border-eleball-outline-variant p-2.5 cursor-pointer hover:bg-eleball-surface-variant">
                    <input
                      type="checkbox"
                      className="mt-0.5"
                      checked={selectedMcps.has(m.name)}
                      onChange={() => toggleSet(setSelectedMcps, m.name)}
                    />
                    <span className="min-w-0">
                      <span className="text-sm font-medium text-eleball-text font-mono">{m.name}</span>
                      <span className="block text-xs text-eleball-text-secondary mt-0.5 font-mono truncate">
                        {m.endpoint || `${m.command} ${(m.args || []).join(' ')}`}
                      </span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
          )}

          <div className="flex flex-wrap items-center gap-3 mt-2">
            <button
              type="button"
              onClick={onImport}
              disabled={importing || (selectedSkills.size === 0 && selectedMcps.size === 0)}
              className="btn-primary text-sm px-5 py-2.5 disabled:opacity-50"
            >
              {importing ? <Loader2 className="w-4 h-4 animate-spin" /> : <DownloadCloud className="w-4 h-4" />}
              导入选中项（{selectedSkills.size + selectedMcps.size}）
            </button>
          </div>

          {importError && (
            <div className="mt-4 text-sm px-3 py-2 rounded-xl bg-red-50 text-red-600">{importError.message}</div>
          )}

          {result && (
            <div className="mt-4 rounded-xl border border-emerald-300 bg-emerald-50 p-4">
              <div className="flex items-center gap-2 text-sm font-semibold text-emerald-700">
                <CheckCircle2 className="w-4 h-4" /> 导入完成
              </div>
              <div className="mt-2 text-xs text-eleball-text-secondary space-y-1">
                {result.skills?.length > 0 && <div>秘技：<span className="font-mono">{result.skills.join(', ')}</span></div>}
                {result.mcps?.length > 0 && <div>MCP：<span className="font-mono">{result.mcps.join(', ')}</span></div>}
                {result.skipped?.length > 0 && (
                  <div className="text-amber-600">跳过：{result.skipped.join('；')}</div>
                )}
              </div>
              <div className="mt-3 text-xs">
                <Link to="/agents" className="text-eleball-primary font-medium hover:underline">
                  前往秘技集市激活使用 →
                </Link>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
