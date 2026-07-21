import { createContext, useContext, useState, useCallback, useEffect } from 'react'
import { getItem, setItem, removeItem, getJSON } from '../utils/storage'
import { authApi } from '../api/client'

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
  }, [])

  const logout = useCallback(() => {
    removeItem('token')
    removeItem('refresh_token')
    removeItem('user')
    setToken(null)
    setUser(null)
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
