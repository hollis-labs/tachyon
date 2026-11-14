import { resolve } from "node:path"
import { defineConfig } from "vitest/config"

// Unit tests only; the browser suite (tests/, Playwright) runs against the real binary.
export default defineConfig({
  resolve: { alias: { "@planner": resolve(import.meta.dirname, "src/planner") } },
  esbuild: { jsx: "automatic" },
  test: { environment: "jsdom", include: ["src/**/*.test.{ts,tsx}"] },
})
