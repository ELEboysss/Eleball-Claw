import { Link } from 'react-router-dom'

export default function Footer() {
  const currentYear = new Date().getFullYear()

  return (
    <footer className="bg-eleball-bg border-t border-eleball-outline-variant py-10">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="flex flex-col md:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-2">
            <img src="/logo-icon.png" alt="Eleball" className="w-6 h-6" />
            <span className="font-semibold text-eleball-text">Eleball</span>
          </div>

          <div className="flex items-center gap-6 text-sm text-eleball-text-secondary">
            <Link to="/" className="hover:text-eleball-primary transition-colors">官网</Link>
            <Link to="/chat" className="hover:text-eleball-primary transition-colors">对话</Link>
            <Link to="/claw-guide" className="hover:text-eleball-primary transition-colors">Claw 指南</Link>
          </div>
        </div>

        <div className="mt-6 pt-6 border-t border-eleball-outline-variant text-center text-xs text-eleball-text-tertiary space-y-1">
          <p>© {currentYear} Eleball. All rights reserved.</p>
          <p>
            <a
              href="https://beian.miit.gov.cn/"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-eleball-primary transition-colors"
            >
              浙ICP备2026049687号-1
            </a>
          </p>
        </div>
      </div>
    </footer>
  )
}
