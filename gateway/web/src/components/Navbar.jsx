import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Menu, X, User, Zap, LogOut } from 'lucide-react'
import { useAuth } from '../context/AuthContext'
import LoginModal from './LoginModal'

const navLinks = [
  { label: '首页', href: '/' },
  { label: '对话', href: '/chat' },
  { label: '视觉', href: '/visual' },
  { label: '模型', href: '/models' },
  { label: '技能', href: '/agents' },
  { label: 'claw 指南', href: '/claw-guide' },
  { label: '文档', href: '/docs' },
]

export default function Navbar() {
  const { isLoggedIn, user, logout, loading } = useAuth()
  const [mobileOpen, setMobileOpen] = useState(false)
  const [loginOpen, setLoginOpen] = useState(false)
  const location = useLocation()

  const handleLogout = () => {
    logout()
    setMobileOpen(false)
  }

  return (
    <>
      <nav className="fixed top-0 left-0 right-0 z-50 bg-eleball-bg/90 backdrop-blur-md border-b border-eleball-outline-variant">
        <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between h-16">
            {/* Logo */}
            <Link to="/" className="flex items-center gap-2 text-eleball-text hover:opacity-80 transition-opacity">
              <img src="/logo-icon.png" alt="Eleball" className="w-8 h-8" />
              <span className="font-bold text-lg tracking-tight">Eleball</span>
            </Link>

            {/* Desktop Nav */}
            <div className="hidden md:flex items-center gap-8">
              {navLinks.map((link) => (
                <Link
                  key={link.href}
                  to={link.href}
                  className={`text-sm font-medium transition-colors ${
                    location.pathname + location.hash === link.href
                      ? 'text-eleball-primary'
                      : 'text-eleball-text-secondary hover:text-eleball-primary'
                  }`}
                >
                  {link.label}
                </Link>
              ))}
            </div>

            {/* User Actions */}
            <div className="hidden md:flex items-center gap-4">
              {loading ? (
                <span className="text-sm text-eleball-text-tertiary">加载中…</span>
              ) : isLoggedIn ? (
                <div className="flex items-center gap-3">
                  <div className="flex items-center gap-2 text-sm text-eleball-text-secondary">
                    <User className="w-4 h-4" />
                    <span className="max-w-[100px] truncate">{user?.nickname || user?.username}</span>
                  </div>
                  <Link
                    to="/recharge"
                    className="inline-flex items-center gap-1 px-3 py-1.5 rounded-full text-xs font-semibold text-eleball-primary bg-eleball-primary-light hover:brightness-95 transition-all"
                  >
                    <Zap className="w-3 h-3" />
                    充值
                  </Link>
                  <button
                    onClick={handleLogout}
                    className="p-2 rounded-full text-eleball-text-secondary hover:bg-eleball-surface-variant transition-colors"
                    title="退出登录"
                  >
                    <LogOut className="w-4 h-4" />
                  </button>
                </div>
              ) : (
                <button
                  onClick={() => setLoginOpen(true)}
                  className="btn-primary text-sm px-5 py-2"
                >
                  免费试用
                </button>
              )}
            </div>

            {/* Mobile Menu Button */}
            <button
              onClick={() => setMobileOpen(!mobileOpen)}
              className="md:hidden p-2 rounded-full text-eleball-text-secondary hover:bg-eleball-surface-variant"
            >
              {mobileOpen ? <X className="w-6 h-6" /> : <Menu className="w-6 h-6" />}
            </button>
          </div>
        </div>

        {/* Mobile Menu */}
        {mobileOpen && (
          <div className="md:hidden border-t border-eleball-outline-variant bg-eleball-bg px-4 py-4 space-y-3">
            {navLinks.map((link) => (
              <Link
                key={link.href}
                to={link.href}
                onClick={() => setMobileOpen(false)}
                className="block text-base font-medium text-eleball-text-secondary hover:text-eleball-primary"
              >
                {link.label}
              </Link>
            ))}
            <div className="pt-3 border-t border-eleball-outline-variant">
              {isLoggedIn ? (
                <div className="flex flex-col gap-3">
                  <div className="flex items-center gap-2 text-sm text-eleball-text-secondary">
                    <User className="w-4 h-4" />
                    <span>{user?.nickname || user?.username}</span>
                  </div>
                  <Link
                    to="/recharge"
                    onClick={() => setMobileOpen(false)}
                    className="btn-primary text-sm justify-center"
                  >
                    <Zap className="w-4 h-4" />
                    充值
                  </Link>
                  <button
                    onClick={handleLogout}
                    className="flex items-center gap-2 text-sm text-eleball-text-secondary hover:text-eleball-error"
                  >
                    <LogOut className="w-4 h-4" />
                    退出登录
                  </button>
                </div>
              ) : (
                <button
                  onClick={() => {
                    setMobileOpen(false)
                    setLoginOpen(true)
                  }}
                  className="btn-primary text-sm w-full justify-center"
                >
                  免费试用
                </button>
              )}
            </div>
          </div>
        )}
      </nav>

      <LoginModal open={loginOpen} onClose={() => setLoginOpen(false)} />
    </>
  )
}
