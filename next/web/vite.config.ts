import { writeFileSync } from "node:fs"
import { resolve } from "node:path"
import tailwindcss from "@tailwindcss/vite"
import { defineConfig } from "vite"

const outDir = resolve(import.meta.dirname, "../internal/webui/dist")
// The dev proxy target: a next/ binary started for development (next/docs/conversion-notes.md).
const backend = process.env.TACHYON_DEV_BACKEND ?? "http://127.0.0.1:8093"

export default defineConfig({
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
  esbuild: { jsx: "automatic" },
  // app-shell ships TypeScript source; the dependency optimizer needs the same JSX runtime.
  optimizeDeps: { esbuildOptions: { jsx: "automatic" } },
  build: { outDir, emptyOutDir: true },
  server: { port: 5179, proxy: { "/api": backend } },
})
