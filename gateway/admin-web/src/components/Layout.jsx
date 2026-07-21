import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

// claw 本地控制台导航（裁剪自云端 admin：去用户/订单/提现/审核/充值/VIP/CDK）
const navItems = [
  { path: '/dashboard', label: '本地控制台', icon: '📊' },
  { path: '/billing', label: '我的计费', icon: '💰' },
  { path: '/eleagent-models', label: '模型配置', icon: '🤖' },
  { path: '/modules', label: '本地模块', icon: '🧩' },
  { path: '/settings', label: '设置', icon: '⚙️' },
]

export default function Layout() {
  const { user, logout } = useAuth()
  const navigate = useNavigate()

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-eleball-bg">
      {/* 侧边栏 */}
      <aside className="w-64 bg-white border-r border-eleball-outline flex flex-col">
        <div className="h-16 flex items-center gap-3 px-6 border-b border-eleball-outline">
          <div className="w-8 h-8 rounded-full bg-gradient-to-br from-eleball-primary-light via-eleball-primary to-eleball-primary-dark" />
          <div className="leading-tight">
            <div className="font-bold text-base tracking-tight">Eleball claw</div>
            <div className="text-[10px] text-eleball-text-secondary">本地控制台</div>
          </div>
        </div>

        <nav className="flex-1 py-4 px-3 space-y-1">
          {navItems.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-medium transition-colors ${
                  isActive
                    ? 'bg-eleball-primary/10 text-eleball-primary-dark'
                    : 'text-eleball-text-secondary hover:bg-gray-50 hover:text-eleball-text'
                }`
              }
            >
              <span className="text-lg">{item.icon}</span>
              {item.label}
            </NavLink>
          ))}
        </nav>

        <div className="p-4 border-t border-eleball-outline">
          <div className="flex items-center gap-3 mb-3">
            <div className="w-9 h-9 rounded-full bg-eleball-primary/10 flex items-center justify-center text-eleball-primary font-bold text-sm">
              {user?.username?.[0]?.toUpperCase() || 'U'}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium truncate">{user?.username || '用户'}</p>
              <p className="text-xs text-eleball-text-secondary">本地账户</p>
            </div>
          </div>
          <button
            onClick={handleLogout}
            className="w-full py-2 rounded-xl text-sm font-medium text-eleball-text-secondary hover:bg-gray-50 transition-colors"
          >
            退出登录
          </button>
        </div>
      </aside>

      {/* 主内容区 */}
      <main className="flex-1 flex flex-col overflow-hidden">
        <header className="h-16 bg-white border-b border-eleball-outline flex items-center justify-between px-8">
          <h2 className="text-lg font-semibold">Eleball claw 本地控制台</h2>
          <div className="flex items-center gap-4 text-sm text-eleball-text-secondary">
            <span>{new Date().toLocaleDateString('zh-CN')}</span>
            <span className="px-2 py-1 rounded-lg bg-eleball-primary/10 text-eleball-primary-dark text-xs font-semibold">
              claw
            </span>
          </div>
        </header>

        <div className="flex-1 overflow-auto p-8">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
