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
          success: '#22C55E'
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
