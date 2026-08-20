import { fileURLToPath, URL } from 'node:url'
import { readFileSync } from 'node:fs'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import vueJsx from '@vitejs/plugin-vue-jsx'
import vueDevTools from 'vite-plugin-vue-devtools'

// 后端开发地址（cmd/gateway 默认监听 :8080，可经 VITE_BACKEND_URL 覆盖）。
const BACKEND_URL = process.env.VITE_BACKEND_URL ?? 'http://localhost:8080'
const pkg = JSON.parse(readFileSync(new URL('./package.json', import.meta.url), 'utf-8')) as {
  version?: string
}

// CI 构建时通过 VITE_APP_VERSION 注入日期版本号（如 1.0.202606141200），
// 本地开发时 fallback 到 package.json 的 version 字段。
const appVersion = process.env.VITE_APP_VERSION ?? pkg.version ?? '0.0.0'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), vueJsx(), vueDevTools()],
  define: {
    __APP_VERSION__: JSON.stringify(appVersion),
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  build: {
    // 图表库与代码编辑器体积大，单独分包并通过懒加载，避免拖大首包。
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (
            id.includes('node_modules/apexcharts') ||
            id.includes('node_modules/vue3-apexcharts')
          ) {
            return 'apexcharts'
          }
          // CodeMirror 6 模块化，单独 chunk 由 CodeEditor 懒加载。
          if (
            id.includes('node_modules/@codemirror/') ||
            id.includes('node_modules/codemirror') ||
            id.includes('node_modules/@lezer/')
          ) {
            return 'codemirror'
          }
        },
      },
    },
    // apexcharts/codemirror 懒加载独立 chunk 体积较大；不计入首屏，提高阈值避免误报。
    chunkSizeWarningLimit: 1200,
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
