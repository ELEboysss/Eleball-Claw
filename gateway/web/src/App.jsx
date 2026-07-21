import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import Navbar from './components/Navbar'
import Footer from './components/Footer'
import Home from './pages/Home'
import Chat from './pages/Chat'
import Recharge from './pages/Recharge'
import Models from './pages/Models'
import AgentMarket from './pages/AgentMarket'
import VisualStudio from './pages/VisualStudio'
import Docs from './pages/Docs'
import Privacy from './pages/Privacy'
import Terms from './pages/Terms'

function App() {
  const location = useLocation()
  const isChat = location.pathname === '/chat'
  const isVisual = location.pathname === '/visual'

  return (
    <div className="min-h-screen flex flex-col bg-eleball-bg text-eleball-text">
      <Navbar />
      <main className={`flex-1 pt-16 flex flex-col min-h-0 ${isChat || isVisual ? 'overflow-hidden' : ''}`}>
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/chat" element={<Chat />} />
          <Route path="/recharge" element={<Recharge />} />
          <Route path="/models" element={<Models />} />
          <Route path="/agents" element={<AgentMarket />} />
          <Route path="/visual" element={<VisualStudio />} />
          <Route path="/video" element={<Navigate to="/visual?tab=video" replace />} />
          <Route path="/docs" element={<Docs />} />
          <Route path="/privacy" element={<Privacy />} />
          <Route path="/terms" element={<Terms />} />
        </Routes>
      </main>
      {!isChat && !isVisual && <Footer />}
    </div>
  )
}

export default App
