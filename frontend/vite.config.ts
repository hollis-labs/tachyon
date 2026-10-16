/// <reference types="vitest/config" />
import path from "node:path"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

const root = path.resolve(__dirname, "..")
const apiTarget = process.env.TACHYON_API_ORIGIN || "http://127.0.0.1:18093"

export default defineConfig({
  base: "/",
  plugins: [tailwindcss()],
  esbuild: { jsx: "automatic" },
  resolve: {
    // Plugin-scoped aliases: each plugin UI keeps its own `@<id>/` root so
    // ported modules never collide on a shared `@/`.
    alias: {
      "@planner": path.join(root, "plugins/planner/ui/src"),
      "@console": path.join(root, "plugins/console/ui/src"),
    },
    dedupe: ["react", "react-dom", "react-router-dom"],
  },
  build: { outDir: "../internal/webui/dist", emptyOutDir: true },
  server: {
    port: Number(process.env.TACHYON_UI_PORT || 5179),
    strictPort: true,
    proxy: { "/api": { target: apiTarget, changeOrigin: true } },
  },
  test: {
    environment: "jsdom",
    include: [
      "../plugins/*/ui/src/**/*.test.{ts,tsx}",
      "../packages/*/src/**/*.test.{ts,tsx}",
      "src/**/*.test.{ts,tsx}",
    ],
  },
})
