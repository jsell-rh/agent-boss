import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath, URL } from 'node:url'

// https://vite.dev/config/
export default defineConfig({
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.ts'],
    coverage: {
      provider: 'v8',
      include: ['src/**/*.ts', 'src/**/*.vue'],
      exclude: ['src/test/**', 'src/main.ts'],
    },
  },
  plugins: [vue(), tailwindcss()],
  build: {
    // Output directly into the Go embed directory so `go build` picks it up.
    // Run `npm run build` inside frontend/ before running `go build`.
    outDir: '../internal/coordinator/frontend',
    // Don't auto-empty: the dir is outside the project root and contains
    // .gitkeep which must persist for go:embed to compile on fresh checkouts.
    emptyOutDir: false,
  },
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
