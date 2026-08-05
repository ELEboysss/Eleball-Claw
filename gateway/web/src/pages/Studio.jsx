import { Link, useSearchParams } from 'react-router-dom'
import { Wand2, DownloadCloud, Wrench } from 'lucide-react'
import useSEO from '../hooks/useSEO'
import ModuleGenerator from './ModuleGenerator'
import MCPInstall from './MCPInstall'

// DIY 工作室：造秘技的统一入口。左侧侧边栏在两种模式间切换——
// 「写脚本造秘技」从零编写 stdio MCP 脚本生成 user_local 模块（原 /module-generator）；
// 「安装远端 MCP」一键安装现成 stdio/http MCP server（原 /mcp-install）。
// 两种模式共享 Studio 的页头与外壳，子组件只负责各自的表单卡片。

const TABS = [
  { key: 'write', label: '写脚本造秘技', desc: '从零编写 stdio MCP 脚本，探测工具并生成模块', icon: Wand2, href: '/studio' },
  { key: 'install', label: '安装远端 MCP', desc: '一键安装现成的 stdio/http MCP server', icon: DownloadCloud, href: '/studio?tab=install' },
]

export default function Studio() {
  useSEO('DIY工作室', '自己动手造秘技：从零编写脚本生成模块，或一键安装现成的远端 MCP server。')
  const [searchParams] = useSearchParams()
  const tab = searchParams.get('tab') === 'install' ? 'install' : 'write'

  return (
    <div className="max-w-5xl mx-auto px-4 sm:px-6 py-8">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-eleball-text flex items-center gap-2">
          <Wrench className="w-6 h-6 text-eleball-primary" /> DIY工作室
        </h1>
        <p className="text-sm text-eleball-text-secondary mt-1">
          自己动手造秘技：从零编写脚本生成模块，或一键安装现成的远端 MCP server。
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
          {tab === 'install' ? <MCPInstall /> : <ModuleGenerator />}
        </div>
      </div>
    </div>
  )
}
