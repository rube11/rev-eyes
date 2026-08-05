import { defineConfig, loadEnv } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const configEnv = loadEnv(mode, process.cwd(), '')
  const allowedHosts = (configEnv.DEV_ALLOWED_HOSTS ?? '')
    .split(',')
    .map((host) => host.trim())
    .filter(Boolean)
  const apiProxyTarget = configEnv.DEV_API_PROXY_TARGET?.trim()
  const apiProxy = apiProxyTarget
    ? {
        '/auth': { target: apiProxyTarget },
        '/workspace': { target: apiProxyTarget },
        '/ws': { target: apiProxyTarget, ws: true },
      }
    : undefined

  return {
    plugins: [react()],
    server: {
      allowedHosts,
      proxy: apiProxy,
    },
  }
})
