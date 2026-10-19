import { consolePlugin } from "@tachyon/console-ui"
import type { TachyonPlugin } from "@tachyon/host-sdk"
import { plannerPlugin } from "@tachyon/planner-ui"

/**
 * Compiled-in plugin UIs (phase 1). Order sets shell-layer nesting: earlier
 * plugins wrap later ones. Runtime loading replaces this list (CW-20261016-0165).
 */
export const plugins: TachyonPlugin[] = [consolePlugin, plannerPlugin]
