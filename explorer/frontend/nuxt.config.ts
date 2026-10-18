import tailwindcss from '@tailwindcss/vite'

// SPA for Wails: no SSR, static output in dist/ (embedded by main.go), relative asset URLs.
export default defineNuxtConfig({
  compatibilityDate: '2026-09-01',
  ssr: false,
  devtools: { enabled: false },
  css: ['~/assets/css/main.css'],
  app: {
    baseURL: '/',
    head: { title: 'OW Storage Explorer', htmlAttrs: { lang: 'en' } },
  },
  nitro: { output: { publicDir: 'dist' } },
  vite: { plugins: [tailwindcss()] },
  telemetry: false,
})
