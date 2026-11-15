/**
 * Agent Panel grants: whether an agent may change a view the user changed. Scoped to one agent in one thread
 * (`scope` = `<agent>|<thread>`). "This session" lasts until the page is closed; "Always" is kept in this browser and
 * listed (with Reset) in Settings.
 */
export type GrantChoice = 'once' | 'session' | 'always' | 'deny'

const KEY = 'console.agentPanel.grants'
const session = new Set<string>()
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((l) => l())

export const scopeKey = (agent: string | undefined, thread: string | undefined) => `${agent ?? 'agent'}|${thread ?? 'thread'}`

function readAlways(): string[] {
  try { const v = JSON.parse(localStorage.getItem(KEY) ?? '[]'); return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : [] } catch { return [] }
}
function writeAlways(list: string[]) {
  try { localStorage.setItem(KEY, JSON.stringify(list)) } catch { /* storage is optional */ }
  emit()
}

export const alwaysGrants = readAlways
export const isGranted = (scope: string) => session.has(scope) || readAlways().includes(scope)
export function grant(scope: string, choice: Exclude<GrantChoice, 'once' | 'deny'>) {
  if (choice === 'session') { session.add(scope); emit() } else if (!readAlways().includes(scope)) writeAlways([...readAlways(), scope])
}
export const resetGrant = (scope: string) => { session.delete(scope); writeAlways(readAlways().filter((s) => s !== scope)) }
export const resetAllGrants = () => { session.clear(); writeAlways([]) }
export const subscribeGrants = (l: () => void) => { listeners.add(l); return () => { listeners.delete(l) } }
