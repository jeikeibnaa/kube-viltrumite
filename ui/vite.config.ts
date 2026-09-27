import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// `npm run dev` serves the UI on :5173 and proxies /api to the operator, so the
// browser sees one origin and the server needs no CORS headers. The target is
// 127.0.0.1 rather than localhost: the operator binds IPv4 loopback by default
// (--ui-bind-address), and localhost can resolve to ::1 first.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8082',
    },
  },
})
