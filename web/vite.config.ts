import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时把接口与 WebRTC 信令转发给本机 Go 后端，保持与生产同源。
const backend = 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    target: 'es2022',
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: backend, changeOrigin: true },
      '/rtc': { target: backend, changeOrigin: true },
    },
  },
})
