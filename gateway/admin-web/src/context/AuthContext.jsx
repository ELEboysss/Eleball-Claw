import { createContext, useContext, useState, useCallback } from 'react'

const AuthContext = createContext(null)

// 安全解析 localStorage 中的 JSON，避免 "undefined" 等非法内容导致崩溃
function safeJSONParse(raw, fallback = null) {
  if (!raw || raw === 'undefined' || raw === 'null') return fallback
  try {
    return JSON.parse(raw)
  } catch {
    return fallback
  }
}

export function AuthProvider({ children }) {
  const [token, setToken] = useState(() => localStorage.getItem('admin_token') || null)
  const [user, setUser] = useState(() => safeJSONParse(localStorage.getItem('admin_user')))

  const login = useCallback((data) => {
    // data: { access_token, refresh_token, user }
    const accessToken = data?.access_token || ''
    const refreshToken = data?.refresh_token || ''
    const userData = data?.user || null

    localStorage.setItem('admin_token', accessToken)
    localStorage.setItem('admin_refresh_token', refreshToken)
    localStorage.setItem('admin_user', JSON.stringify(userData))
    setToken(accessToken)
    setUser(userData)
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem('admin_token')
    localStorage.removeItem('admin_refresh_token')
    localStorage.removeItem('admin_user')
    setToken(null)
    setUser(null)
  }, [])

  return (
    <AuthContext.Provider value={{ token, user, login, logout, isLoggedIn: !!token }}>
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used within AuthProvider')
  return ctx
}
