import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/spaces': {
        target: 'http://localhost:8899',
        // SSE endpoints under /spaces/{space}/events need streaming
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes) => {
            // Disable buffering for SSE (text/event-stream)
            if (proxyRes.headers['content-type']?.includes('text/event-stream')) {
              proxyRes.headers['cache-control'] = 'no-cache'
              proxyRes.headers['x-accel-buffering'] = 'no'
            }
          })
        },
      },
      '/events': {
        target: 'http://localhost:8899',
        configure: (proxy) => {
          proxy.on('proxyRes', (proxyRes) => {
            if (proxyRes.headers['content-type']?.includes('text/event-stream')) {
              proxyRes.headers['cache-control'] = 'no-cache'
              proxyRes.headers['x-accel-buffering'] = 'no'
            }
          })
        },
      },
      '/api': 'http://localhost:8899',
      '/raw': 'http://localhost:8899',
      '/agent': 'http://localhost:8899',
    },
  },
})
