import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import Navbar from './components/Navbar'
import Footer from './components/Footer'
import Home from './pages/Home'
import Chat from './pages/Chat'
import Recharge from './pages/Recharge'
import Models from './pages/Models'
import AgentMarket from './pages/AgentMarket'
import Assistants from './pages/Assistants'
import VisualStudio from './pages/VisualStudio'
import ClawGuide from './pages/ClawGuide'

function App() {
  const location = useLocation()
  const isChat = location.pathname === '/chat'
  const isVisual = location.pathname === '/visual'
  // 内嵌云端内容的页面：iframe 撑满视口，隐藏 Footer，与 Chat/Visual 同为全屏视图。
  const isEmbed = ['/', '/recharge'].includes(location.pathname)
  const isFullView = isChat || isVisual || isEmbed

  return (
    <div className="min-h-screen flex flex-col bg-eleball-bg text-eleball-text">
      <Navbar />
      <main className={`flex-1 pt-16 flex flex-col min-h-0 ${isFullView ? 'overflow-hidden' : ''}`}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/chat" element={<Chat />} />
          <Route path="/recharge" element={<Recharge />} />
          <Route path="/models" element={<Models />} />
          <Route path="/agents" element={<AgentMarket />} />
          <Route path="/assistants" element={<Assistants />} />
          <Route path="/visual" element={<VisualStudio />} />
          <Route path="/video" element={<Navigate to="/visual?tab=video" replace />} />
          <Route path="/claw-guide" element={<ClawGuide />} />
        </Routes>
      </main>
      {!isFullView && <Footer />}
    </div>
  )
}

export default App
