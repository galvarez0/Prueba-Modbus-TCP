import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
    // If you want to run UI locally while calling dockerized admin-api:
    // set VITE_API_BASE to http://localhost:8095
    proxy: {
      "/api": {
        target: "http://127.0.0.1:8095",
        changeOrigin: true
      }
    }
  }
});
    