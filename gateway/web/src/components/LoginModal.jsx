import { useState, useRef } from 'react'
import { X } from 'lucide-react'
import { useAuth } from '../context/AuthContext'
import { authApi } from '../api/client'
import { getDeviceId } from '../utils/storage'

export default function LoginModal({ open, onClose }) {
  // 登录 / 注册切换
  const [isRegister, setIsRegister] = useState(false)

  // 账号密码表单（登录用「用户名或邮箱」）
  const [identifier, setIdentifier] = useState('')
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  // 注册邮箱验证码
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
      const data = isRegister
        ? await authApi.register(username, password, email, otpCode, deviceId)
        : await authApi.login(identifier, password, deviceId)

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

  const switchMode = () => {
    setIsRegister(!isRegister)
    setError('')
    setConfirmPassword('')
    setOtpCode('')
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

        <form onSubmit={handleSubmit} className="space-y-4">
          {isRegister ? (
            <>
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
                <label className="block text-sm font-medium text-eleball-text-secondary mb-1">邮箱验证码</label>
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
            </>
          ) : (
            <div>
              <label className="block text-sm font-medium text-eleball-text-secondary mb-1">用户名或邮箱</label>
              <input
                type="text"
                value={identifier}
                onChange={(e) => setIdentifier(e.target.value)}
                className="input"
                placeholder="请输入用户名或邮箱"
                required
              />
            </div>
          )}

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

        <div className="mt-5 text-center text-sm">
          <span className="text-eleball-text-secondary">{isRegister ? '已有账号？' : '还没有账号？'}</span>
          <button
            onClick={switchMode}
            className="ml-1 text-eleball-primary font-semibold hover:underline"
          >
            {isRegister ? '立即登录' : '立即注册'}
          </button>
        </div>

        {isRegister && (
          <p className="mt-3 text-xs text-eleball-text-tertiary text-center">
            注册需用户名、密码、邮箱三者必备，邮箱须经验证码验证。
          </p>
        )}
      </div>
    </div>
  )
}
