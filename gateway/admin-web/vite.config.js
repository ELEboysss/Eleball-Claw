import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  // 部署在 /admin/ 子路径下
  base: '/admin/',
  server: {
    proxy: {
      '/api': {
        // 本地开发/E2E 时指向本机 Gateway；生产构建不依赖此代理
        target: process.env.E2E_API_URL || 'http://localhost:8080',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/api/, '/v1')
      }
    }
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    chunkSizeWarningLimit: 700
  }
})
