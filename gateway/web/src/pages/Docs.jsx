import { useEffect } from 'react'
import useSEO from '../hooks/useSEO'
import { CLOUD_BASE } from '../api/client'
import { BookOpen, ArrowRight } from 'lucide-react'

// claw 文档页：整页跳转云端 eleball.cn/docs（claw 不维护本地文档副本，统一读云端）。
// 见 docs/marketing/claw-implementation-plan.md §C.2。
export default function Docs() {
  useSEO('文档', '跳转至云端文档')
  useEffect(() => {
    window.location.replace(`${CLOUD_BASE}/docs`)
  }, [])
  return (
    <div className="flex-1 flex items-center justify-center px-4 py-24">
      <div className="text-center max-w-md">
        <BookOpen className="w-10 h-10 text-eleball-primary mx-auto mb-4" />
        <h1 className="text-xl font-bold text-eleball-text mb-2">正在跳转到文档</h1>
        <p className="text-sm text-eleball-text-secondary mb-6">
          claw 文档统一由云端 eleball.cn 提供。若未自动跳转，请点击下方按钮。
        </p>
        <a
          href={`${CLOUD_BASE}/docs`}
          className="btn-primary text-sm px-5 py-2 inline-flex items-center gap-2"
        >
          前往文档 <ArrowRight className="w-4 h-4" />
        </a>
      </div>
    </div>
  )
}
