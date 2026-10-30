/**
 * Every user-facing string of the Agents monitor. Components in agents-monitor
 * take their words from here, so a rename is one change.
 */
import type { SessionState } from './types'

export const AGENTS = {
  title: 'Agents',
  tabs: { sessions: 'Sessions', activity: 'Activity', teams: 'Teams' },
  tabsLabel: 'Agent views',
  launch: 'Launch',
  collecting: (n: number, last: string) => `collecting · ${n} sessions · last event ${last}`,
  notCollecting: 'Not collecting: Tether is unreachable',
  reconnecting: 'Reconnecting. Showing the last data, read-only',
  loadFailed: 'Could not load the sessions',
  retry: 'Retry',
  stale: 'stale data',
  searchLabel: 'Search sessions, tools and tasks',
  searchPlaceholder: 'Search sessions, tools, tasks',
  filtersLabel: 'Session filters',
  viewLabel: 'Session view',
  views: { rows: 'Rows', cards: 'Cards', board: 'Board' },
  filters: { all: 'All', working: 'Working', needs_you: 'Needs you', stale: 'Stale' },
  states: { working: 'Working', waiting: 'Waiting', idle: 'Idle', stale: 'Stale', unknown: 'Unknown' } as Record<SessionState, string>,
  needsYou: { approval: 'Needs approval', question: 'Has a question' },
  staleReasons: { no_events: 'No events', not_alive: 'Not alive', worker_offline: 'Worker offline' },
  idleFor: (age: string) => `idle ${age}`,
  summaryLabel: 'Sessions by state',
  summaryText: (c: Record<SessionState, number>) => `${c.working} working, ${c.waiting} waiting, ${c.idle} idle, ${c.stale + c.unknown} stale or unknown`,
  empty: 'No agents running',
  noMatch: 'No sessions match',
  clearFilters: 'Clear filters',
  groupBy: 'Group by',
  group: { workroot: 'Workspace', agent: 'Agent', none: 'None' },
  sessionsList: 'Sessions',
  noActivity: 'no activity yet',
  contextLabel: (pct: number | null) => (pct === null ? 'Context unknown' : `Context ${pct}%`),
  contextUnknown: 'context unknown',
  uptime: 'up',
  kind: { chat: 'chat', process: 'process' },
  tasks: (n: number) => `${n} tasks`,
  boardCols: { working: 'Working', needs_you: 'Needs you', idle: 'Idle', stale: 'Stale' },
  boardLane: (t: string, n: number) => `${t}, ${n} sessions`,
  boardEmpty: (t: string) => `No sessions in ${t}`,
  open: 'Open',
} as const

export type FilterId = keyof typeof AGENTS.filters
