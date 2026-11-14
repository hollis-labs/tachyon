import type { FrontendManifest } from "@hollis-labs/app-shell"
import generated from "./generated/frontend.manifest.json"

/** `substrate frontend` merges the modules' items with the app's own (next/cmd/tachyon/ui/app.json). */
export const manifest = generated as unknown as FrontendManifest

/** The surface routes, for mapping a deep link to the area that owns it (surfaces.ts). */
export const surfaceRoutes: readonly string[] = (
  (generated as { items?: { kind?: string; route?: string }[] }).items ?? []
)
  .filter((i) => i.kind === "surface" && typeof i.route === "string")
  .map((i) => i.route as string)
