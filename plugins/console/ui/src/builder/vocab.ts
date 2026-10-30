// The one vocabulary module for Builder nouns. Components import words from here.
export type BuilderTab = 'agents' | 'teams' | 'launches' | 'library' | 'projects'
export type AgentState = 'active' | 'disabled' | 'retired'
export type PolicyTag = 'ask' | 'edits' | 'bypass' | 'plan'

export const BUILDER_TITLE = 'Builder'
export const DEFINITIONS_NOTE = 'Definitions only. Running agents are on'
export const NOT_REPORTED = 'not reported'
export const INHERITS = 'inherits'

export const TAB_LABEL: Record<BuilderTab, string> = { agents: 'Agents', teams: 'Teams', launches: 'Launches', library: 'Library', projects: 'Projects' }
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

export const LIBRARY_KINDS = [
  { id: 'prompts', label: 'Prompts' },
  { id: 'skills', label: 'Skills' },
  { id: 'tool_servers', label: 'Tool servers' },
  { id: 'policies', label: 'Policies' },
] as const
export const TAB_PATH: Record<BuilderTab, string> = { agents: '/builder/agents', teams: '/builder/teams', launches: '/builder/launches', library: '/builder/library/prompts', projects: '/registry/projects' }
