import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  server: { port: 5173, host: true },
    // If you want to run UI locally while calling dockerized admin-api:
    // set VITE_API_BASE to http://localhost:8095
})
