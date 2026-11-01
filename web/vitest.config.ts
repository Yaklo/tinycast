import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'

// 前端单元测试独立配置：不影响生产构建（vite.config.ts）。
export default defineConfig({
  plugins: [vue()],
  test: {
    environment: 'happy-dom',
    include: ['src/**/*.spec.ts'],
    restoreMocks: true,
    // 默认 5 秒在开发机上偶尔会被后台任务拖爆（组件挂载 + 动态 import 都要时间），
    // 放宽到 15 秒，避免把「机器忙」当成「代码坏」。
    testTimeout: 15000,
    hookTimeout: 15000,
  },
})
