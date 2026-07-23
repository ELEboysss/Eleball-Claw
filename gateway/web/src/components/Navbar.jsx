import { useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Menu, X, User, Zap, LogOut } from 'lucide-react'
import { useAuth } from '../context/AuthContext'
import LoginModal from './LoginModal'

// claw 本地导航栏：紫底白高亮（与官网 iframe 内的白底紫高亮导航栏区分，提示用户「这是 claw 本地壳」）。
// 底色 bg-eleball-primary(#6750A4)；当前链接白、其余白/70；CTA（充值/免费试用）用白底紫字药丸。
// 字号/按钮比官网大一档，更瞩目。

const navLinks = [
  { label: '官网', href: '/' },
  { label: '对话', href: '/chat' },
  { label: '视觉', href: '/visual' },
  { label: '模型', href: '/models' },
  { label: '技能', href: '/agents' },
  { label: 'Claw 指南', href: '/claw-guide' },
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
      <nav className="fixed top-0 left-0 right-0 z-50 bg-eleball-primary border-b border-eleball-primary-dark/30">
        <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between h-16">
            {/* Logo */}
            <Link to="/" className="flex items-center text-white hover:opacity-80 transition-opacity">
              <span className="font-bold text-xl tracking-tight">Eleball-Claw</span>
            </Link>

            {/* Desktop Nav */}
            <div className="hidden md:flex items-center gap-8">
              {navLinks.map((link) => (
                <Link
                  key={link.href}
                  to={link.href}
                  className={`text-base font-medium transition-colors ${
                    location.pathname + location.hash === link.href
                      ? 'text-white'
                      : 'text-white/70 hover:text-white'
                  }`}
                >
                  {link.label}
                </Link>
              ))}
            </div>

            {/* User Actions */}
            <div className="hidden md:flex items-center gap-4">
              {loading ? (
                <span className="text-base text-white/60">加载中…</span>
              ) : isLoggedIn ? (
                <div className="flex items-center gap-3">
                  <div className="flex items-center gap-2 text-base text-white/80">
                    <User className="w-5 h-5" />
                    <span className="max-w-[100px] truncate">{user?.nickname || user?.username}</span>
                  </div>
                  <Link
                    to="/recharge"
                    className="inline-flex items-center gap-1.5 px-4 py-2 rounded-full text-sm font-semibold text-eleball-primary bg-white hover:bg-white/90 transition-colors"
                  >
                    <Zap className="w-4 h-4" />
                    充值
                  </Link>
                  <button
                    onClick={handleLogout}
                    className="p-2.5 rounded-full text-white/80 hover:bg-white/10 transition-colors"
                    title="退出登录"
                  >
                    <LogOut className="w-5 h-5" />
                  </button>
                </div>
              ) : (
                <button
                  onClick={() => setLoginOpen(true)}
                  className="inline-flex items-center justify-center gap-2 rounded-full font-semibold text-eleball-primary bg-white hover:bg-white/90 transition-colors text-base px-6 py-2.5"
                >
                  免费试用
                </button>
              )}
            </div>

            {/* Mobile Menu Button */}
            <button
              onClick={() => setMobileOpen(!mobileOpen)}
              className="md:hidden p-2 rounded-full text-white/80 hover:bg-white/10"
            >
              {mobileOpen ? <X className="w-6 h-6" /> : <Menu className="w-6 h-6" />}
            </button>
          </div>
        </div>

        {/* Mobile Menu */}
        {mobileOpen && (
          <div className="md:hidden border-t border-white/10 bg-eleball-primary px-4 py-4 space-y-3">
            {navLinks.map((link) => (
              <Link
                key={link.href}
                to={link.href}
                onClick={() => setMobileOpen(false)}
                className="block text-lg font-medium text-white/80 hover:text-white"
              >
                {link.label}
              </Link>
            ))}
            <div className="pt-3 border-t border-white/10">
              {isLoggedIn ? (
                <div className="flex flex-col gap-3">
                  <div className="flex items-center gap-2 text-base text-white/80">
                    <User className="w-5 h-5" />
                    <span>{user?.nickname || user?.username}</span>
                  </div>
                  <Link
                    to="/recharge"
                    onClick={() => setMobileOpen(false)}
                    className="inline-flex items-center justify-center gap-2 rounded-full font-semibold text-eleball-primary bg-white hover:bg-white/90 transition-colors text-base px-6 py-2.5"
                  >
                    <Zap className="w-5 h-5" />
                    充值
                  </Link>
                  <button
                    onClick={handleLogout}
                    className="flex items-center gap-2 text-base text-white/80 hover:text-white"
                  >
                    <LogOut className="w-5 h-5" />
                    退出登录
                  </button>
                </div>
              ) : (
                <button
                  onClick={() => {
                    setMobileOpen(false)
                    setLoginOpen(true)
                  }}
                  className="inline-flex items-center justify-center gap-2 rounded-full font-semibold text-eleball-primary bg-white hover:bg-white/90 transition-colors text-base w-full px-6 py-2.5"
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
