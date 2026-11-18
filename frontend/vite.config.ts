import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      // Dev wiring: same-origin /api hits the Vite dev server and is proxied
      // to the Go backend — no CORS in development. In production the SPA is
      // static on Vercel and calls VITE_API_URL directly (CORS-allowlisted
      // on the backend via CORS_ALLOWED_ORIGINS).
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
    },
  },
});
