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
      chunkSizeWarningLimit: 1600,
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
