import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { MotionConfig } from 'framer-motion'
import { AuthProvider } from './context/AuthContext'
import { ChatProvider } from './context/ChatContext'
import ErrorBoundary from './components/ErrorBoundary'
import App from './App'
// T-PR-A1：JetBrains Mono 等宽技术体（本地打包 woff2，font-display:swap）。
// 用于命令 / 代码 / 快捷键等 font-mono 场景；400 常规 + 600 强调。
import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/600.css'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary>
        <AuthProvider>
          <ChatProvider>
            {/* UX-O13：reducedMotion="user" 让 framer-motion 动画尊重系统「减少动态效果」设置，
                CSS 动画由 index.css 的 prefers-reduced-motion 媒体查询接管。 */}
            <MotionConfig reducedMotion="user">
              <App />
            </MotionConfig>
          </ChatProvider>
        </AuthProvider>
      </ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
