/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // 对齐 App 端 ChatBubbleWindow 的 Material 3 默认色板，整体保持极简白紫
        eleball: {
          bg: '#FFFFFF',
          surface: '#FFFFFF',
          'surface-variant': '#F3F0F7',
          primary: '#6750A4',
          'primary-light': '#EADDFF',
          'primary-dark': '#21005D',
          secondary: '#625B71',
          'secondary-light': '#E8DEF8',
          tertiary: '#7D5260',
          text: '#1C1B1F',
          'text-secondary': '#49454F',
          'text-tertiary': '#79747E',
          outline: '#E2E0E5',
          'outline-variant': '#F0EDF4',
          error: '#B3261E',
          success: '#22C55E',
          // UX-O14：视觉工作室 dark 主题 token（统一原硬编码 hex，便于主题维护）
          'vs-bg': '#13131f',              // 画布/媒体占位背景
          'vs-surface': '#1c1c2b',         // 面板/卡片/遮罩
          'vs-surface-variant': '#252538', // 输入框/未激活按钮底
          'vs-border': '#26263a',          // 面板分隔
          'vs-border-variant': '#2e2e45',  // 输入框/虚线边框
          'vs-text': '#e8e8f0',            // 主文本
          'vs-text-muted': '#a0a0b8',      // 次要文本/标签
          'vs-text-dim': '#6e6e8a',        // 三级文本/占位符
          'vs-accent': '#b8a5ff',          // 选中/悬停浅紫强调
          'vs-primary-hover': '#4a3b7a',   // 主按钮悬停
          'vs-error': '#ff7b7b'            // 错误/删除悬停红
        }
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'PingFang SC', 'Noto Sans SC', 'Microsoft YaHei', 'sans-serif']
      },
      animation: {
        'breathe': 'breathe 3s ease-in-out infinite',
        'float': 'float 6s ease-in-out infinite',
        'fade-in-up': 'fadeInUp 0.8s ease-out forwards',
      },
      keyframes: {
        breathe: {
          '0%, 100%': { transform: 'scale(1)', boxShadow: '0 8px 32px rgba(103,80,164,0.25)' },
          '50%': { transform: 'scale(1.05)', boxShadow: '0 12px 48px rgba(103,80,164,0.35)' },
        },
        float: {
          '0%, 100%': { transform: 'translateY(0px)' },
          '50%': { transform: 'translateY(-12px)' },
        },
        fadeInUp: {
          '0%': { opacity: '0', transform: 'translateY(24px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        }
      }
    },
  },
  plugins: [],
}
