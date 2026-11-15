import { resolve } from "node:path"
import { defineConfig } from "vitest/config"

// Unit tests only; the browser suite (tests/, Playwright) runs against the real binary.
export default defineConfig({
  resolve: {
    alias: {
      "@planner": resolve(import.meta.dirname, "src/planner"),
      "@console": resolve(import.meta.dirname, "src/console"),
      "@tachyon/torque-client": resolve(
        import.meta.dirname,
        "src/packages/torque-client/src/index.ts",
      ),
      "@tachyon/host-sdk": resolve(import.meta.dirname, "src/packages/host-sdk/src/index.ts"),
    },
  },
  esbuild: { jsx: "automatic" },
  test: { environment: "jsdom", include: ["src/**/*.test.{ts,tsx}"] },
})
