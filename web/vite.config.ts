import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueJsx from '@vitejs/plugin-vue-jsx'
import vueDevTools from 'vite-plugin-vue-devtools'

// 后端开发地址（cmd/gateway 默认监听 :8080，可经 VITE_BACKEND_URL 覆盖）。
const BACKEND_URL = process.env.VITE_BACKEND_URL ?? 'http://localhost:8080'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), vueJsx(), vueDevTools()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    // 开发态把后端路由代理到 Go 网关，避免 Vite SPA fallback 把
    // /api/* 等路径吞成 index.html（导致前端拿到 HTML 而非 JSON）。
    proxy: {
      '/api': { target: BACKEND_URL, changeOrigin: true },
      '/mcp': { target: BACKEND_URL, changeOrigin: true, ws: true },
      '/healthz': { target: BACKEND_URL, changeOrigin: true },
    },
  },
})
