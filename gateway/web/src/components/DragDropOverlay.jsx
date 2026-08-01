import { motion, AnimatePresence } from 'framer-motion'
import { Upload } from 'lucide-react'

// DragDropOverlay 拖拽文件时的整区遮罩。
// ui-skills baseline-ui：仅动画 transform/opacity；<200ms ease-out；
// 不用 backdrop-blur（大面 backdrop-filter 动画）；固定 z-40（介于内容 z-30 与侧栏/弹窗 z-50 之间）。
export default function DragDropOverlay({ visible }) {
  return (
    <AnimatePresence>
      {visible && (
        <motion.div
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          transition={{ duration: 0.15, ease: 'easeOut' }}
          className="absolute inset-0 z-40 flex items-center justify-center bg-eleball-surface/90 pointer-events-none"
          aria-hidden="true"
        >
          <motion.div
            initial={{ opacity: 0, scale: 0.96 }}
            animate={{ opacity: 1, scale: 1 }}
            exit={{ opacity: 0, scale: 0.96 }}
            transition={{ duration: 0.15, ease: 'easeOut' }}
            className="flex flex-col items-center gap-3 px-8 py-6 rounded-2xl border-2 border-dashed border-eleball-primary bg-white shadow-sm"
          >
            <Upload className="w-8 h-8 text-eleball-primary" />
            <p className="text-sm font-medium text-eleball-text text-pretty">松开以添加图片或文件</p>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  )
}
