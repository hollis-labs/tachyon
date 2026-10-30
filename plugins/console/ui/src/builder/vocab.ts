// The one vocabulary module for Builder nouns. Components import words from here.
export type BuilderTab = 'agents' | 'launches' | 'library'
export type AgentState = 'active' | 'disabled' | 'retired'
export type PolicyTag = 'ask' | 'edits' | 'bypass' | 'plan'

export const BUILDER_TITLE = 'Builder'
export const DEFINITIONS_NOTE = 'Definitions only. Running agents are on'
export const NOT_REPORTED = 'not reported'
export const INHERITS = 'inherits'

export const TAB_LABEL: Record<BuilderTab, string> = { agents: 'Agents', launches: 'Launches', library: 'Library' }
export const STATE_LABEL: Record<AgentState, string> = { active: 'Active', disabled: 'Disabled', retired: 'Retired' }
export const POLICY_LABEL: Record<PolicyTag, string> = { ask: 'Ask', edits: 'Edits', bypass: 'Bypass', plan: 'Plan' }

export const SECTIONS = [
  { id: 'identity', label: 'Identity' },
  { id: 'runtime', label: 'Runtime and launch' },
  { id: 'prompt', label: 'Prompt' },
  { id: 'tools', label: 'Tools and skills' },
  { id: 'policy', label: 'Policy' },
  { id: 'resolved', label: 'Resolved' },
  { id: 'used_by', label: 'Used by' },
  { id: 'checks', label: 'Checks' },
  { id: 'history', label: 'History' },
] as const
export type SectionId = (typeof SECTIONS)[number]['id']
