import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In development the API runs on :8081; in Docker nginx proxies /api instead.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: { "/api": process.env.API_URL ?? "http://localhost:8081" },
  },
});
