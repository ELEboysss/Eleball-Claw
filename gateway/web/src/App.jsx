import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import Navbar from './components/Navbar'
import Footer from './components/Footer'
import Chat from './pages/Chat'
import Recharge from './pages/Recharge'
import Models from './pages/Models'
import AgentMarket from './pages/AgentMarket'
import VisualStudio from './pages/VisualStudio'
import ClawGuide from './pages/ClawGuide'
import TeamDetail from './pages/TeamDetail'
import Studio from './pages/Studio'

function App() {
  const location = useLocation()
  const isChat = location.pathname === '/chat' || location.pathname.startsWith('/chat/')
  const isVisual = location.pathname === '/visual'
  // 内嵌云端内容的页面：iframe 撑满视口，隐藏 Footer，与 Chat/Visual 同为全屏视图。
  const isEmbed = location.pathname === '/recharge'
  const isFullView = isChat || isVisual || isEmbed

  return (
    <div className="min-h-screen flex flex-col bg-eleball-bg text-eleball-text">
      <Navbar />
      <main className={`flex-1 pt-16 flex flex-col min-h-0 ${isFullView ? 'overflow-hidden' : ''}`}>
        <Routes>
          <Route path="/" element={<Navigate to="/chat" replace />} />
          <Route path="/chat" element={<Chat />} />
          <Route path="/chat/:conversationId" element={<Chat />} />
          <Route path="/recharge" element={<Recharge />} />
          <Route path="/models" element={<Models />} />
          <Route path="/agents" element={<AgentMarket />} />
          <Route path="/studio" element={<Studio />} />
          {/* 旧路径回兼容：原 /module-generator、/mcp-install 已并入 /studio 侧边栏 */}
          <Route path="/module-generator" element={<Navigate to="/studio" replace />} />
          <Route path="/mcp-install" element={<Navigate to="/studio?tab=install" replace />} />
          <Route path="/visual" element={<VisualStudio />} />
          <Route path="/video" element={<Navigate to="/visual?tab=video" replace />} />
          <Route path="/claw-guide" element={<ClawGuide />} />
          <Route path="/teams/:teamId" element={<TeamDetail />} />
        </Routes>
      </main>
      {!isFullView && <Footer />}
    </div>
  )
}

export default App
