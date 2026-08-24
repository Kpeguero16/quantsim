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
    // The API, proxied so `npm run dev` is same-origin exactly like the
    // deployed stack is. Step 26 moved the app to relative URLs; without this
    // the dev server would serve the bundle and then 404 every API call
    // against itself.
    //
    // This list is the gateway's routing table and has to match the one in
    // infra/docker/Caddyfile. A route added to the gateway needs a line in
    // both, and the failure when it is missing here is a 404 from Vite that
    // reads like a broken backend.
    //
    // /backtests appears twice on purpose: the collection is requested at
    // exactly that path, and a prefix pattern alone would not match it.
    proxy: Object.fromEntries(
      ['/auth', '/market-data', '/trading', '/backtests', '/insights'].map(
        (prefix) => [prefix, { target: 'http://localhost:8080', changeOrigin: false }],
      ),
    ),
  },
})
