/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      colors: {
        eleball: {
          primary: '#14B8A6',
          'primary-light': '#5DE8D4',
          'primary-dark': '#0D9488',
          secondary: '#F59E0B',
          tertiary: '#8B5CF6',
          bg: '#F8FAFC',
          surface: '#FFFFFF',
          'surface-dark': '#1E293B',
          text: '#0F172A',
          'text-secondary': '#64748B',
          outline: '#E2E8F0'
        }
      },
      fontFamily: {
        sans: ['-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'PingFang SC', 'Noto Sans SC', 'Microsoft YaHei', 'sans-serif']
      }
    },
  },
  plugins: [],
}
