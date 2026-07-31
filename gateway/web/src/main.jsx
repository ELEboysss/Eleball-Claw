import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { MotionConfig } from 'framer-motion'
import { AuthProvider } from './context/AuthContext'
import { ChatProvider } from './context/ChatContext'
import App from './App'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <ChatProvider>
          {/* UX-O13：reducedMotion="user" 让 framer-motion 动画尊重系统「减少动态效果」设置，
              CSS 动画由 index.css 的 prefers-reduced-motion 媒体查询接管。 */}
          <MotionConfig reducedMotion="user">
            <App />
          </MotionConfig>
        </ChatProvider>
      </AuthProvider>
    </BrowserRouter>
  </React.StrictMode>,
)
