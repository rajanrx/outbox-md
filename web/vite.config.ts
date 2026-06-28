import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// In docker dev the backend is reachable as the `backend` service; natively it's
// localhost. Override with VITE_API_TARGET.
const apiTarget = process.env.VITE_API_TARGET || "http://localhost:8181";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    proxy: {
      "/api": apiTarget,
      "/mcp": apiTarget,
    },
  },
  build: { outDir: "dist" },
  test: { environment: "node" },
});
