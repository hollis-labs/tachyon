/**
 * Every user-facing Planner noun, status and sentence of the new GUI lives
 * here, so the verb and kind alignment is one change. Shared components take
 * their strings from props; they never import this file.
 */
import type { PlannerDb } from '@planner/api/types'

export const LENSES: ReadonlyArray<{ db: PlannerDb; label: string; key: string }> = [
  { db: 'themes', label: 'Themes', key: 't' },
  { db: 'workstreams', label: 'Streams', key: 's' },
  { db: 'roadmap', label: 'Roadmap', key: 'r' },
  { db: 'ideas', label: 'Ideas', key: 'i' },
  { db: 'decisions', label: 'Decisions', key: 'd' },
  { db: 'risks', label: 'Risks', key: 'k' },
  { db: 'inbox', label: 'Inbox', key: 'x' },
]

export const LANES = [
  { id: 'todo', title: 'To do' },
  { id: 'doing', title: 'Doing' },
  { id: 'review', title: 'Review' },
  { id: 'blocked', title: 'Blocked' },
  { id: 'done', title: 'Done' },
] as const
export type LaneId = (typeof LANES)[number]['id']

export const TEXT = {
  appName: 'Planner',
  lensesLabel: 'Planner lenses',
  spineLabel: 'Spine',
  detailsLabel: 'Details',
  needYou: 'Need you',
  needYouEmpty: 'Nothing needs you',
  clearFilter: 'Clear',
  board: 'Board',
  timeline: 'Timeline',
  viewLabel: 'Focus view',
  moveTo: 'Move to',
  back: 'Spine',
  pin: 'Pin',
  pinned: 'Pinned',
  saving: 'Saving',
  undo: 'Undo',
  moved: (task: string, lane: string) => `Moved ${task} to ${lane}`,
  writeFailed: 'That did not save',
  retry: 'Retry',
  selectItem: 'Select a theme to see where it stands',
  loadFailed: 'Could not load the planner',
  turnedOff: 'The planner is switched off',
  turnedOffHint: 'Turn it on from the planner settings to use it.',
  progressText: (done: number, total: number, blocked: number) => `${done} of ${total} done${blocked ? `, ${blocked} blocked` : ''}`,
  noTasks: 'No tasks counted yet',
  momentum: 'Momentum',
  momentumPlaceholder: 'Momentum read comes from the backend',
  links: 'Links',
  linksEmpty: 'Nothing linked yet',
  decisions: 'Decisions',
  decisionsEmpty: 'No decisions waiting',
  activity: 'Activity',
  activityEmpty: 'No comments yet',
  commentPlaceholder: 'Comment or update',
  commentSend: 'Send',
  timelineEmpty: 'Nothing has happened yet. Approve the theme to start the timeline',
  timelineFilteredEmpty: 'Nothing matches',
  timelineFacets: 'Timeline filters',
  timelineStream: 'Events, newest first',
  milestones: 'Milestones',
  fixtureNote: 'Sample data until the backend lands',
  lane: (title: string, n: number) => `${title}, ${n} tasks`,
  laneEmpty: (title: string) => `Drop a task here to ${title}`,
  dragHint: 'Draggable task. Space to pick up, arrows to move between lanes, Space to drop, Escape to cancel',
  pickedUp: (c: string, l: string) => `Picked up ${c} from ${l}`,
  movedOver: (c: string, l: string) => `${c} is over ${l}`,
  dropped: (c: string, l: string) => `Dropped ${c} in ${l}`,
  cancelled: (c: string) => `Cancelled moving ${c}`,
  paletteTitle: 'Planner palette',
  paletteEmpty: 'Nothing matches',
  modeCreate: 'Create',
  modeOpen: 'Open',
  createTheme: 'Theme',
  createIdea: 'Idea',
  linkTo: 'Link to…',
  linkKindTitle: 'How are they related?',
  linkKindHint: 'Press 1 to 4, or Esc to cancel',
  linked: (kind: string, other: string) => `${kind} ${other}`,
  linkAdded: (from: string, kind: string, to: string) => `Linked ${from} ${kind} ${to}`,
  linkPort: (title: string) => `Link ${title}`,
  linkDragging: (title: string) => `Linking ${title}`,
  doneSummaryLabel: 'What shipped?',
  doneSummaryHint: 'Enter adds, Esc skips',
  doneSummaryAdd: 'Add',
  doneSummarySkip: 'Skip',
  mentionLabel: 'Mention a theme',
  keysHint: '/ palette · N new · L link · B board · M timeline · P pin · F finish · G then T/S/R/I/D/K/X lens',
} as const

export const LINK_KINDS = [
  { id: 'needs', label: 'Needs' },
  { id: 'delivers', label: 'Delivers' },
  { id: 'mitigates', label: 'Mitigates' },
  { id: 'relates', label: 'Relates to' },
] as const
export type LinkKind = (typeof LINK_KINDS)[number]['id']

export const FACETS = [
  { id: 'all', label: 'All' },
  { id: 'tasks', label: 'Tasks' },
  { id: 'prs', label: 'PRs' },
  { id: 'decisions', label: 'Decisions' },
  { id: 'blockers', label: 'Blockers' },
  { id: 'comments', label: 'Comments' },
  { id: 'workstreams', label: 'Workstreams' },
] as const
