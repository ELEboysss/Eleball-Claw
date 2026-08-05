import { motion } from 'framer-motion'

// PageHero -- 功能页共享标题区（taste-skill §4.7/§4.1）。
// 排版纪律：display tracking-tight + ≤25 词副标 + 可选 1 eyebrow，无卡箱。
// 每页独立 eyebrow 预算（1 页 1 hero eyebrow 合规，§4.7）。
const EASE = [0.16, 1, 0.3, 1]

export default function PageHero({ eyebrow, title, subtitle, align = 'left' }) {
  const centered = align === 'center'
  return (
    <div className={`mb-8 ${centered ? 'text-center' : ''}`}>
      {eyebrow && (
        <motion.span
          initial={{ opacity: 0, y: 12 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.5, ease: EASE }}
          className="eyebrow"
        >
          {eyebrow}
        </motion.span>
      )}
      <motion.h1
        initial={{ opacity: 0, y: 16 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay: 0.08, duration: 0.6, ease: EASE }}
        className={`display text-3xl sm:text-4xl text-eleball-text text-balance ${eyebrow ? 'mt-4' : ''}`}
      >
        {title}
      </motion.h1>
      {subtitle && (
        <motion.p
          initial={{ opacity: 0, y: 16 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ delay: 0.16, duration: 0.6, ease: EASE }}
          className={`mt-3 text-eleball-text-secondary text-pretty ${centered ? 'max-w-2xl mx-auto' : 'max-w-2xl'}`}
        >
          {subtitle}
        </motion.p>
      )}
    </div>
  )
}
