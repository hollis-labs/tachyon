import type { ReactNode } from 'react'

/** A tab inside an Agent Panel. Built-in tabs are rendered by Aside; a registered tab brings its own `render`. */
export interface PanelTab { id: string; label: string; render?: () => ReactNode }

/**
 * An Agent Panel: a named set of tabs. `appliesTo` limits it to agents (by agent id) or roles; omitted means every
 * thread may choose it. `owner` says who defined it: the system (shipped, or contributed by a plugin) or the user.
 */
export interface PanelDef { id: string; label: string; owner: 'system' | 'user'; tabs: PanelTab[]; appliesTo?: { agents?: string[]; roles?: string[] } }

/** The existing sidebar, migrated as-is. Every thread has it; it is the default. */
export const DEFAULT_PANEL_ID = 'agent'
export const DEFAULT_PANEL: PanelDef = {
  id: DEFAULT_PANEL_ID,
  label: 'Agent Panel',
  owner: 'system',
  tabs: [
    { id: 'plan', label: 'Plan' }, { id: 'topics', label: 'Topics' }, { id: 'bookmarks', label: 'Bookmarks' }, { id: 'work', label: 'Work' },
    { id: 'activity', label: 'Activity' }, { id: 'approvals', label: 'Approvals' }, { id: 'files', label: 'Files' }, { id: 'doc', label: 'Artifacts' },
  ],
}

const extra = new Map<string, PanelDef>()
const listeners = new Set<() => void>()
let version = 0

/** Register (or replace) a panel. Plugins and user config call this; the Agent Panel host re-reads on change. Returns an unregister. */
export function registerPanel(def: PanelDef): () => void {
  if (def.id === DEFAULT_PANEL_ID) throw new Error(`panel id "${DEFAULT_PANEL_ID}" is reserved for the default Agent Panel`)
  extra.set(def.id, def)
  version++
  listeners.forEach((l) => l())
  return () => { if (extra.get(def.id) === def) { extra.delete(def.id); version++; listeners.forEach((l) => l()) } }
}

export const subscribePanels = (l: () => void) => { listeners.add(l); return () => { listeners.delete(l) } }
export const panelsVersion = () => version

export interface PanelScope { agentId?: string; role?: string }

/** Panels that may show for this thread: the default first, then each registered panel whose `appliesTo` matches. */
export function panelsFor(scope: PanelScope): PanelDef[] {
  const fits = (p: PanelDef) => !p.appliesTo || (!!scope.agentId && !!p.appliesTo.agents?.includes(scope.agentId)) || (!!scope.role && !!p.appliesTo.roles?.includes(scope.role))
  return [DEFAULT_PANEL, ...[...extra.values()].filter(fits)]
}

/** The panel a thread shows: the user's stored pick when it still applies, else the first panel that names this agent or role, else the default. */
export function resolvePanel(scope: PanelScope, stored: string | null): PanelDef {
  const all = panelsFor(scope)
  return all.find((p) => p.id === stored) ?? all.find((p) => p.appliesTo) ?? DEFAULT_PANEL
}

const KEY = (thread: string) => `console.agentPanel.${thread}`
/** The user's panel pick per thread, kept in this browser. */
export function storedPanel(thread?: string): string | null {
  if (!thread) return null
  try { return localStorage.getItem(KEY(thread)) } catch { return null }
}
export function storePanel(thread: string | undefined, id: string) {
  if (!thread) return
  try { localStorage.setItem(KEY(thread), id) } catch { /* storage is optional */ }
}
