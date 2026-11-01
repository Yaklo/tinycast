import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// 前端单元测试独立配置：不影响生产构建（vite.config.ts）。
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.spec.ts'],
    restoreMocks: true,
  },
})
