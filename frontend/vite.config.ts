import { writeFileSync } from "node:fs"
import { join } from "node:path"
import { fileURLToPath } from "node:url"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, type Plugin } from "vite"

const distDir = fileURLToPath(new URL("../internal/webui/dist/", import.meta.url))

// internal/webui/embed.go embeds this output with `//go:embed all:dist`,
// which is a compile-time error when the pattern matches nothing.
// emptyOutDir wipes the directory on every build, so restore the placeholder
// here so bare `npm run build` preserves the tracked .gitkeep file.
function keepDistTracked(): Plugin {
  return {
    name: "tachyon-keep-dist-tracked",
    apply: "build",
    closeBundle() {
      writeFileSync(join(distDir, ".gitkeep"), "")
    },
  }
}

// The Go binary serves this SPA under base_path (see internal/webui).
// `build.outDir` points at the Go embed directory so `npm run build`
// drops the bundle exactly where `//go:embed all:dist` expects it.
export default defineConfig({
  base: "/sysop/",
  plugins: [react(), tailwindcss(), keepDistTracked()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
  },
  server: {
    // `make ui-dev` proxies same-origin /api calls to `make run` on :8093.
    proxy: {
      "/api": "http://localhost:8093",
    },
  },
})
