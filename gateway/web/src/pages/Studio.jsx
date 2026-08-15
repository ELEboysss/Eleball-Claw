import { Link, useSearchParams } from 'react-router-dom'
import { DownloadCloud, Wrench, Sparkles, Puzzle, Cable } from 'lucide-react'
import useSEO from '../hooks/useSEO'
import MCPInstall from './MCPInstall'
import DSHPlugin from './DSHPlugin'
import DSHRuntimeImport from './DSHRuntimeImport'
import SkillGenerator from './SkillGenerator'

// DIY 工作室：秘技生态接入的统一入口（导入导向）。左侧侧边栏在四种导入形式间切换——
// 「MCP 安装」搜索社区注册表或粘贴配置，一键安装现成 stdio/http MCP server（E1/G3）；
// 「DSH 运行时」把 DSH 工具插件经 dsh-mcp-bridge 挂载为实时可调用的秘技运行时（T5）；
// 「DSH 插件」输入 npm 包名，扫描并导入 DSH 插件内的 SKILL.md / MCP 配置（F4）；
// 「Skill 加载」编写 Anthropic 标准 SKILL.md 生成 prompt-only 秘技（E4，无需代码）。
// 四种形式共享 Studio 的页头与外壳，子组件只负责各自的表单卡片。
// 说明：对话页「创造模式」可在对话中自动完成上述全部流程（F3）；「写脚本造秘技」已退役，
// 脚本类能力请用创造模式或 MCP 安装接入。

const TABS = [
  { key: 'install', label: 'MCP 安装', desc: '搜索社区市场或粘贴配置，一键安装现成 MCP server', icon: DownloadCloud, href: '/studio' },
  { key: 'dsh-runtime', label: 'DSH 运行时', desc: '一键挂载 DSH 工具插件（fs/shell/web 搜索等）为可调用秘技', icon: Cable, href: '/studio?tab=dsh-runtime' },
  { key: 'dsh', label: 'DSH 插件', desc: '输入 npm 包名，导入 DSH 插件内的秘技与 MCP 配置', icon: Puzzle, href: '/studio?tab=dsh' },
  { key: 'skill', label: 'Skill 加载', desc: '编写 Anthropic 标准 SKILL.md，生成提示词秘技（无需代码）', icon: Sparkles, href: '/studio?tab=skill' },
]

export default function Studio() {
  useSEO('DIY工作室', '接入秘技生态：搜索/安装社区 MCP server、挂载 DSH 工具插件运行时、导入 DSH 插件（npm 包）、编写 SKILL.md 提示词秘技。')
  const [searchParams] = useSearchParams()
  const tabParam = searchParams.get('tab')
  const tab = tabParam === 'dsh' || tabParam === 'skill' || tabParam === 'dsh-runtime' ? tabParam : 'install'

  return (
    <div className="max-w-5xl mx-auto px-4 sm:px-6 py-8">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-eleball-text flex items-center gap-2">
          <Wrench className="w-6 h-6 text-eleball-primary" /> DIY工作室
        </h1>
        <p className="text-sm text-eleball-text-secondary mt-1">
          接入开放秘技生态：安装社区 MCP server、挂载 DSH 工具插件运行时、导入 DSH 插件包内资产，或编写 SKILL.md 提示词秘技。
          也可以在对话页切换到「创造」模式，让 Agent 对话式完成创造与导入。
        </p>
      </div>

      <div className="flex flex-col md:flex-row gap-6">
        {/* 侧边导航：md 以上竖排，移动端横排滚动 */}
        <aside className="md:w-60 flex-shrink-0">
          <nav className="flex md:flex-col gap-2 overflow-x-auto md:overflow-visible pb-1 md:pb-0 -mx-1 px-1">
            {TABS.map((t) => {
              const Icon = t.icon
              const active = tab === t.key
              return (
                <Link
                  key={t.key}
                  to={t.href}
                  className={`flex items-start gap-2 rounded-xl border p-3 transition-colors whitespace-nowrap md:whitespace-normal ${
                    active
                      ? 'border-eleball-primary bg-eleball-primary/5'
                      : 'border-eleball-outline hover:bg-eleball-surface-variant'
                  }`}
                >
                  <Icon className={`w-4 h-4 flex-shrink-0 mt-0.5 ${active ? 'text-eleball-primary' : 'text-eleball-text-secondary'}`} />
                  <div className="min-w-0">
                    <div className={`text-sm font-semibold ${active ? 'text-eleball-primary' : 'text-eleball-text'}`}>{t.label}</div>
                    <div className="hidden md:block text-xs text-eleball-text-secondary mt-0.5">{t.desc}</div>
                  </div>
                </Link>
              )
            })}
          </nav>
        </aside>

        {/* 内容区 */}
        <div className="flex-1 min-w-0">
          {tab === 'dsh-runtime' ? <DSHRuntimeImport /> : tab === 'dsh' ? <DSHPlugin /> : tab === 'skill' ? <SkillGenerator /> : <MCPInstall />}
        </div>
      </div>
    </div>
  )
}
