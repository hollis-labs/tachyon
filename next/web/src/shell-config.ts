import type { ShellActions } from "@hollis-labs/app-shell"
import type { LayoutStorage } from "@hollis-labs/plugin-host-ui"

/**
 * The action gateway, local only: the toolbar's Chat entry and openChat use the shell's own openRegion and
 * closeRegion. No agent function is routed, so the backend and the step-up grant are never reached.
 */
export const actions: ShellActions = {
  backend: { execute: async () => ({ status: "refused", reason: "unsupported-action" }) },
  agentFunctions: [],
  audit: () => {},
  issueGrant: () => Promise.reject(new Error("No action needs a step-up grant here.")),
}

/** The user's dock size pick, remembered per browser as the old drawer's width was. */
export const dockStorage: LayoutStorage = {
  read: (key: string) => localStorage.getItem(key),
  write: (key: string, value: string) => localStorage.setItem(key, value),
  remove: (key: string) => localStorage.removeItem(key),
}
