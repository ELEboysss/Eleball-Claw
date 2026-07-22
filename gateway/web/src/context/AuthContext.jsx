import { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { getItem, setItem, removeItem, getJSON } from '../utils/storage'
import { authApi } from '../api/client'
import { broadcastAuthToCloud, startCloudAuthListener } from '../utils/authSync'

const AuthContext = createContext(null)

export function AuthProvider({ children }) {
  const [token, setToken] = useState(() => getItem('token') || null)
  const [user, setUser] = useState(() => getJSON('user'))
  const [loading, setLoading] = useState(true)

  // 初始化时尝试获取用户信息
  useEffect(() => {
    let cancelled = false
    if (token) {
      authApi.me()
        .then((data) => {
          if (!cancelled) {
            setUser(data)
            setItem('user', JSON.stringify(data))
          }
        })
        .catch(() => {
          // 失败不退出，下次请求会再次触发刷新/重登
        })
        .finally(() => {
          if (!cancelled) setLoading(false)
        })
    } else {
      setLoading(false)
    }
    return () => { cancelled = true }
  }, [token])

  // 收云端 iframe 回传的登录态（iframe 内登录/登出后同步回 claw）。
  // ts 新旧判断在 authSync 内完成；此处只写本地 storage + state，不 rebroadcast，防死循环。
  useEffect(() => {
    return startCloudAuthListener((data) => {
      if (data.token) {
        setItem('token', data.token)
        setItem('refresh_token', data.refresh_token || '')
        if (data.user) setItem('user', JSON.stringify(data.user))
        setToken(data.token)
        setUser(data.user || null)
      } else {
        removeItem('token')
        removeItem('refresh_token')
        removeItem('user')
        setToken(null)
        setUser(null)
      }
    })
  }, [])

  const login = useCallback((data) => {
    const accessToken = data?.access_token || ''
    const refreshToken = data?.refresh_token || ''
    const userData = data?.user || null

    setItem('token', accessToken)
    setItem('refresh_token', refreshToken)
    if (userData) {
      setItem('user', JSON.stringify(userData))
    }
    setToken(accessToken)
    setUser(userData)
    // 同步到内嵌云端官网 iframe
    broadcastAuthToCloud({ token: accessToken, refresh_token: refreshToken, user: userData })
  }, [])

  const logout = useCallback(() => {
    removeItem('token')
    removeItem('refresh_token')
    removeItem('user')
    setToken(null)
    setUser(null)
    // 同步登出到内嵌云端官网 iframe
    broadcastAuthToCloud({ token: null, refresh_token: null, user: null })
  }, [])

  // token 刷新后（client.js 拦截器 dispatch 事件）同步新 token 到 iframe；
  // refresh_token 会轮换，必须同步，否则 iframe 持旧 refresh_token 刷新会失败登出。
  useEffect(() => {
    function onRefresh(e) {
      const { access_token, refresh_token } = e.detail || {}
      if (!access_token) return
      setToken(access_token)
      if (refresh_token) setItem('refresh_token', refresh_token)
      broadcastAuthToCloud({ token: access_token, refresh_token, user: getJSON('user') })
    }
    window.addEventListener('eleball:auth-refreshed', onRefresh)
    return () => window.removeEventListener('eleball:auth-refreshed', onRefresh)
  }, [])

  const updateUser = useCallback((userData) => {
    setUser(userData)
    setItem('user', JSON.stringify(userData))
  }, [])

  return (
    <AuthContext.Provider value={{
      token,
      user,
      login,
      logout,
      updateUser,
      isLoggedIn: !!token,
      loading
    }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
