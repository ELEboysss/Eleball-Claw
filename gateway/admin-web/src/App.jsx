import { Routes, Route, Navigate } from 'react-router-dom'
import { useAuth } from './context/AuthContext'
import Layout from './components/Layout'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import Users from './pages/Users'
import Billing from './pages/Billing'
import Orders from './pages/Orders'
import Withdrawals from './pages/Withdrawals'
import AgentsReview from './pages/AgentsReview'
import EleAgentModels from './pages/EleAgentModels'
import RechargePackages from './pages/RechargePackages'
import VIPPlans from './pages/VIPPlans'
import CDKManagement from './pages/CDKManagement'
import Modules from './pages/Modules'
import Settings from './pages/Settings'

// 受保护路由包装器
function RequireAuth({ children }) {
  const { token } = useAuth()
  return token ? children : <Navigate to="/login" replace />
}

function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/*"
        element={
          <RequireAuth>
            <Layout />
          </RequireAuth>
        }
      >
        <Route index element={<Navigate to="/dashboard" replace />} />
        <Route path="dashboard" element={<Dashboard />} />
        <Route path="users" element={<Users />} />
        <Route path="billing" element={<Billing />} />
        <Route path="orders" element={<Orders />} />
        <Route path="withdrawals" element={<Withdrawals />} />
        <Route path="agents-review" element={<AgentsReview />} />
        <Route path="eleagent-models" element={<EleAgentModels />} />
        <Route path="recharge-packages" element={<RechargePackages />} />
        <Route path="vip-plans" element={<VIPPlans />} />
        <Route path="cdk-management" element={<CDKManagement />} />
        <Route path="modules" element={<Modules />} />
        <Route path="settings" element={<Settings />} />
      </Route>
    </Routes>
  )
}

export default App
