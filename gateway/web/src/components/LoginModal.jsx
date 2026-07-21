import { useState, useRef } from 'react'
import { X } from 'lucide-react'
import { useAuth } from '../context/AuthContext'
import { authApi } from '../api/client'
import { getDeviceId } from '../utils/storage'

export default function LoginModal({ open, onClose }) {
  // tab: 'password' 账号密码 | 'email' 邮箱验证码
  const [tab, setTab] = useState('email')

  // 账号密码表单
  const [isRegister, setIsRegister] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  // 邮箱验证码表单
  const [email, setEmail] = useState('')
  const [otpCode, setOtpCode] = useState('')
  const [otpCooldown, setOtpCooldown] = useState(0)
  const cooldownTimer = useRef(null)

  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const { login, updateUser } = useAuth()

  if (!open) return null

  const startCooldown = () => {
    setOtpCooldown(60)
    if (cooldownTimer.current) clearInterval(cooldownTimer.current)
    cooldownTimer.current = setInterval(() => {
      setOtpCooldown((prev) => {
        if (prev <= 1) {
          clearInterval(cooldownTimer.current)
          return 0
        }
        return prev - 1
      })
    }, 1000)
  }

  const handleSendOTP = async () => {
    setError('')
    if (!email || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) {
      setError('请输入有效邮箱')
      return
    }
    try {
      await authApi.sendEmailOTP(email)
      startCooldown()
    } catch (err) {
      setError(err.message || '验证码发送失败')
    }
  }

  const handleEmailLogin = async (e) => {
    e.preventDefault()
    setError('')

    if (!email || !otpCode) {
      setError('请填写邮箱与验证码')
      return
    }
    setSubmitting(true)
    try {
      const deviceId = getDeviceId()
      const data = await authApi.emailLogin(email, otpCode, deviceId)
      login(data)
      try {
        const user = await authApi.me()
        updateUser({ ...user, default_model_profile: data.default_model_profile })
      } catch {
        // me 接口失败不影响登录
      }
      onClose()
    } catch (err) {
      setError(err.message || '登录失败')
    } finally {
      setSubmitting(false)
    }
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')

    if (isRegister && password !== confirmPassword) {
      setError('两次输入的密码不一致')
      return
    }

    setSubmitting(true)

    try {
      const deviceId = getDeviceId()
      const api = isRegister ? authApi.register : authApi.login
      const data = await api(username, password, deviceId)

      // 先写入 token，让后续 /auth/me 能带上认证
      login(data)

      // 后端登录/注册不直接返回 user 对象，需要再调 /auth/me
      try {
        const user = await authApi.me()
        updateUser({ ...user, default_model_profile: data.default_model_profile })
      } catch {
        // me 接口失败不影响登录
      }

      onClose()
    } catch (err) {
      setError(err.message || (isRegister ? '注册失败' : '登录失败'))
    } finally {
      setSubmitting(false)
    }
  }

  const switchTab = (t) => {
    setTab(t)
    setError('')
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center px-4 bg-black/40 backdrop-blur-sm">
      <div className="relative w-full max-w-sm bg-eleball-surface rounded-3xl p-6 shadow-xl">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 p-1 rounded-full text-eleball-text-tertiary hover:bg-eleball-surface-variant"
        >
          <X className="w-5 h-5" />
        </button>

        <h2 className="text-xl font-bold text-eleball-text mb-1">登录 Eleball</h2>
        <p className="text-sm text-eleball-text-secondary mb-6">登录后体验完整对话与每日免费额度</p>

        {/* Tab 切换 */}
        <div className="flex gap-1 p-1 mb-6 bg-eleball-surface-variant/50 rounded-xl">
          <button
            onClick={() => switchTab('email')}
            className={`flex-1 py-2 rounded-lg text-sm font-medium transition-all ${
              tab === 'email' ? 'bg-eleball-surface text-eleball-primary shadow-sm' : 'text-eleball-text-secondary'
            }`}
          >
            邮箱验证码
          </button>
          <button
            onClick={() => switchTab('password')}
            className={`flex-1 py-2 rounded-lg text-sm font-medium transition-all ${
              tab === 'password' ? 'bg-eleball-surface text-eleball-primary shadow-sm' : 'text-eleball-text-secondary'
            }`}
          >
            账号密码
          </button>
        </div>

        {tab === 'email' ? (
          <form onSubmit={handleEmailLogin} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-eleball-text-secondary mb-1">邮箱</label>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="input"
                placeholder="请输入邮箱"
                required
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-eleball-text-secondary mb-1">验证码</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={otpCode}
                  onChange={(e) => setOtpCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  className="input flex-1"
                  placeholder="6 位验证码"
                  required
                  maxLength={6}
                />
                <button
                  type="button"
                  onClick={handleSendOTP}
                  disabled={otpCooldown > 0}
                  className="btn-secondary px-4 py-2 text-sm whitespace-nowrap disabled:opacity-50"
                >
                  {otpCooldown > 0 ? `${otpCooldown}s` : '获取验证码'}
                </button>
              </div>
            </div>

            {error && (
              <div className="text-sm text-eleball-error bg-red-50 px-3 py-2 rounded-xl">{error}</div>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="btn-primary w-full justify-center disabled:opacity-60"
            >
              {submitting ? '请稍候…' : '登录 / 注册'}
            </button>

            <p className="text-xs text-eleball-text-tertiary text-center">
              验证码 10 分钟内有效。新邮箱将自动注册。
            </p>
          </form>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-sm font-medium text-eleball-text-secondary mb-1">用户名</label>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="input"
                placeholder="请输入用户名"
                required
                minLength={3}
              />
            </div>
            <div>
              <label className="block text-sm font-medium text-eleball-text-secondary mb-1">密码</label>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="input"
                placeholder="请输入密码"
                required
                minLength={6}
              />
            </div>

            {isRegister && (
              <div>
                <label className="block text-sm font-medium text-eleball-text-secondary mb-1">确认密码</label>
                <input
                  type="password"
                  value={confirmPassword}
                  onChange={(e) => setConfirmPassword(e.target.value)}
                  className="input"
                  placeholder="请再次输入密码"
                  required
                  minLength={6}
                />
              </div>
            )}

            {error && (
              <div className="text-sm text-eleball-error bg-red-50 px-3 py-2 rounded-xl">{error}</div>
            )}

            <button
              type="submit"
              disabled={submitting}
              className="btn-primary w-full justify-center disabled:opacity-60"
            >
              {submitting ? '请稍候…' : (isRegister ? '注册' : '登录')}
            </button>
          </form>
        )}

        {tab === 'password' && (
          <div className="mt-5 text-center text-sm">
            <span className="text-eleball-text-secondary">{isRegister ? '已有账号？' : '还没有账号？'}</span>
            <button
              onClick={() => {
                setIsRegister(!isRegister)
                setError('')
                setConfirmPassword('')
              }}
              className="ml-1 text-eleball-primary font-semibold hover:underline"
            >
              {isRegister ? '立即登录' : '立即注册'}
            </button>
          </div>
        )}
      </div>
    </div>
  )
}
