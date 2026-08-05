/** @type {import('tailwindcss').Config} */
// T-PR-A3：对齐 cloud web（gateway/web）PR-A 设计 token -- 紫色 primary + 完整中性阶。
// hex 而非 oklch：tailwind 3.4.19 对 oklch 静默丢弃 /N 透明度变体（~多处 eleball-primary/10 会失效），
// hex 仍生成 rgb(... / 0.N)；:root oklch（index.css）与此处 hex 数值等价、注释交叉引用。
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        // 对齐 App 端 Material 3 白紫极简色板（与 gateway/web/tailwind.config.js 一致）
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
        sans: ['-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'PingFang SC', 'Noto Sans SC', 'Microsoft YaHei', 'sans-serif'],
        // T-PR-A3：JetBrains Mono 优先（@fontsource 本地打包），系统等宽体兜底。
        mono: ['JetBrains Mono', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'Liberation Mono', 'Courier New', 'monospace']
      }
    },
  },
  plugins: [],
}
