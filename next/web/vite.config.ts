import { writeFileSync } from "node:fs"
import { resolve } from "node:path"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

const outDir = resolve(import.meta.dirname, "../internal/webui/dist")
/**
 * The dev proxy target: a next/ binary you started for development, on its own port and data
 * directory. There is deliberately no default: a guessed address could be a running Tachyon with
 * real data. `npm run dev` refuses to start without it; `npm run build` does not need it.
 */
function devBackend(): string {
  const backend = process.env.TACHYON_DEV_BACKEND
  if (!backend) {
    throw new Error(
      "TACHYON_DEV_BACKEND is not set. Start a next/ binary on a scratch port and data directory, then run e.g. TACHYON_DEV_BACKEND=http://127.0.0.1:<port> npm run dev",
    )
  }
  return backend
}

export default defineConfig(({ command }) => ({
  base: "/",
  plugins: [
    tailwindcss(),
    {
      // emptyOutDir removes dist/.gitkeep, which the Go embed needs to compile when unbuilt.
      name: "keep-gitkeep",
      closeBundle() {
        writeFileSync(resolve(outDir, ".gitkeep"), "")
      },
    },
  ],
  // The planner keeps its own import root (carried over from plugins/planner/ui unchanged).
  resolve: { alias: { "@planner": resolve(import.meta.dirname, "src/planner") } },
  esbuild: { jsx: "automatic" },
  // app-shell ships TypeScript source; the dependency optimizer needs the same JSX runtime.
  optimizeDeps: { esbuildOptions: { jsx: "automatic" } },
  build: {
    outDir,
    emptyOutDir: true,
    rollupOptions: {
      // React libraries mark modules "use client"; that directive means nothing in a client-only
      // bundle, and Rollup warns once per module. Every other warning still prints.
      onwarn(warning, warn) {
        if (warning.code === "MODULE_LEVEL_DIRECTIVE") return
        warn(warning)
      },
    },
  },
  server: command === "serve" ? { port: 5179, proxy: { "/api": devBackend() } } : undefined,
}))
