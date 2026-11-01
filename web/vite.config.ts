import { defineConfig } from 'vite'
import type { Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发时把接口与 WebRTC 信令转发给本机 Go 后端，保持生产同源。
const backend = 'http://127.0.0.1:18855'

// Vite 8 的压缩阶段会删掉依赖里的版权注释（`esbuild.legalComments` 已经不起作用），
// 所以在产物写盘前补一条归属声明；完整许可文本见 THIRD-PARTY-NOTICES.md。
const LICENSE_BANNER = `/*! TinyCast | MIT License
 * 前端包含 hls.js（Apache-2.0，© 2017 Dailymotion）与 Vue（MIT，© Yuxi You）。
 * 完整许可文本见发布包内的 THIRD-PARTY-NOTICES.md 与 LICENSE。
 */
`

function licenseBanner(): Plugin {
  return {
    name: 'tinycast-license-banner',
    apply: 'build',
    // 必须放在 generateBundle：更早的 renderChunk 结果会被压缩器再处理一遍，注释会被删掉。
    generateBundle(_options, bundle) {
      for (const item of Object.values(bundle)) {
        if (item.type === 'chunk') item.code = LICENSE_BANNER + item.code
      }
    },
  }
}

export default defineConfig({
  plugins: [vue(), licenseBanner()],
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
