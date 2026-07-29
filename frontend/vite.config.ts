import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    // The gateway's allowed CORS origin is the hardcoded constant
    // "http://localhost:5173" (services/gateway/cmd/server/main.go). If this
    // port is taken, Vite's default behaviour is to quietly move to 5174 --
    // and then every API call fails CORS, which reads as a broken gateway
    // rather than a busy port. strictPort turns that into a loud startup
    // failure instead. See SPEC.md 2.4.
    port: 5173,
    strictPort: true,
  },
})
