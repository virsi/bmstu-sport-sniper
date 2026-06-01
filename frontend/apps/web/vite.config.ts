import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'node:path'

/**
 * Vite-конфиг для apps/web.
 *
 * Особенности:
 * - alias `@` указывает на `src` (используется во всех импортах внутри SPA).
 * - dev-proxy `/api` пробрасывает на `gateway-svc` (по умолчанию `:8080`).
 * - SSE endpoint `/api/stream` тоже проксируется (отдельная запись с `ws: false`,
 *   чтобы не было путаницы с WebSocket-апгрейдом — у нас server-sent events).
 */
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api/stream': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: false,
        // SSE требует отключённой буферизации
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes) => {
            proxyRes.headers['cache-control'] = 'no-cache'
          })
        },
      },
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    target: 'es2022',
    sourcemap: true,
  },
})
