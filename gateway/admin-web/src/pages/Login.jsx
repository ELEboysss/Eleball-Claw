import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { authApi } from '../api/client'

// claw 控制台登录：用用户账户（调云端统一登录接口），非管理员专属。
// 登录后 token 两端通用（claw JWT_SECRET 需与云端一致）。
export default function Login() {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const { login } = useAuth()
  const navigate = useNavigate()

  const handleSubmit = async (e) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      // 调云端统一登录（与 web/APP 同账户体系），device_id 标记 claw 控制台
      const res = await authApi.login(username, password)
      login(res)
      navigate('/dashboard')
    } catch (err) {
      console.error('login error:', err)
      setError(typeof err === 'string' ? err : (err?.message || '登录失败，请检查账号密码'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-eleball-bg">
      <div className="w-full max-w-md">
        <div className="text-center mb-8">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-gradient-to-br from-eleball-primary-light via-eleball-primary to-eleball-primary-dark shadow-lg" />
          <h1 className="text-2xl font-bold tracking-tight">Eleball claw</h1>
          <p className="text-eleball-text-secondary mt-1">本地控制台登录</p>
        </div>

        <div className="card">
          {error && (
            <div className="mb-4 p-3 rounded-xl bg-red-50 text-red-600 text-sm">
              {error}
            </div>
          )}
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label className="block text-sm font-medium mb-1.5">用户名</label>
              <input
                type="text"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                className="input"
                placeholder="你的 Eleball 账户"
                required
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5">密码</label>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="input"
                placeholder="••••••••"
                required
              />
            </div>
            <button
              type="submit"
              disabled={loading}
              className="btn-primary w-full disabled:opacity-50 disabled:hover:translate-y-0"
            >
              {loading ? '登录中...' : '登录'}
            </button>
          </form>
        </div>

        <p className="text-center text-xs text-eleball-text-secondary mt-6">
          使用你的 Eleball 账户登录，与云端统一账户
        </p>
      </div>
    </div>
  )
}
