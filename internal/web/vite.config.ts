import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:20128",
      "/v1": "http://localhost:20128",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
  },
});