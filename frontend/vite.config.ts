import { fileURLToPath, URL } from 'node:url'

import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import nightwatchPlugin from 'vite-plugin-nightwatch'

const defaultPort = 8855
const port = process.env.SIDE_SERVER_PORT || defaultPort.toString()

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [
    vue(),
    nightwatchPlugin(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    proxy: {
      '/api': {
        target: `http://localhost:${port}`,
        changeOrigin: true,
        secure: false,
      },
      '/ws': {
        target: `http://localhost:${port}`,
        changeOrigin: true,
        secure: false,
        ws: true,
      }
    }
  },
  build: {
    // Note: most of this is from the syntax-highlighting for diffs, which
    // likely can be optimized a whole lot as it appears to be included multiple
    // times (not 100% sure)
    // TODO /gen/req/plan optimize the frontend total build size (don't care
    // about chunking just yet)
    chunkSizeWarningLimit: 2048,
  },
  test: {
    onConsoleLog(log: string, type: 'stdout' | 'stderr'): false | void {
      //console.log('log in test: ', log);
      if (log === 'message from third party library' && type === 'stdout') {
        return false;
      }
    },
  }
})
