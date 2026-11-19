// Extension points for the autolink renderer. Feature modules (G2: forge
// PR/commit/repo; R2: app-wide rollout) call these at startup, next to their
// `registry.register(kind)`, so a new kind gets an icon, extra quick actions
// and a richer drawer body without touching R1 code. All registries are
// module-level and return an unregister function.
import type { ComponentType, ReactNode } from 'react'
import {
  Bot, BookOpen, Brain, ClipboardList, FileText, FolderKanban, GitBranch, GitCommitHorizontal, GitPullRequest,
  Layers, Link as LinkIcon, ListChecks, Milestone, Workflow, ExternalLink, type LucideIcon,
} from 'lucide-react'
import type { OpenTarget, PreviewCard, RefIdentity } from '../refs'

// ---------- icons ----------

const icons = new Map<string, LucideIcon>(Object.entries({
  task: ListChecks, epic: Layers, project: FolderKanban, sprint: Milestone, plan: ClipboardList,
  memory: Brain, agent: Bot, workstream: Workflow, file: FileText, url: ExternalLink, link: LinkIcon,
  // Pre-registered for G2 so forge kinds can name them without importing lucide.
  pr: GitPullRequest, commit: GitCommitHorizontal, repo: BookOpen, branch: GitBranch,
}))

/** Map an icon token (`RefKind.icon`) to a glyph. Unknown tokens fall back to a link icon. */
export function registerRefIcon(token: string, icon: LucideIcon): () => void {
  icons.set(token, icon)
  return () => {
    if (icons.get(token) === icon) icons.delete(token)
  }
}
export function refIcon(token: string): LucideIcon {
  return icons.get(token) ?? LinkIcon
}

// ---------- quick actions ----------

/**
 * Phase 1 is read-only: actions navigate, open externally or copy. A mutating
 * action kind (approve, assign) is a later addition and must go through the
 * console ActionBus and IdentityConfirm, not through this type.
 */
export type RefAction =
  | { id: string; label: string; type: 'open'; target: Extract<OpenTarget, { type: 'route' | 'external' }> }
  | { id: string; label: string; type: 'copy'; text: string }

export type RefActionProvider = (ref: RefIdentity, card: PreviewCard) => RefAction[]

const actionProviders = new Map<string, RefActionProvider[]>()

/** Extra quick actions for a kind, appended after the defaults (open, copy id). */
export function registerRefActions(kind: string, provider: RefActionProvider): () => void {
  actionProviders.set(kind, [...(actionProviders.get(kind) ?? []), provider])
  return () => {
    const rest = (actionProviders.get(kind) ?? []).filter((p) => p !== provider)
    if (rest.length) actionProviders.set(kind, rest)
    else actionProviders.delete(kind)
  }
}

const inPlaceKinds = new Set<string>()

/** Mark a kind as read entirely in its drawer: it offers no Open page action. */
export function markRefInPlace(kind: string): () => void {
  inPlaceKinds.add(kind)
  return () => {
    inPlaceKinds.delete(kind)
  }
}

export function refActions(ref: RefIdentity, card: PreviewCard): RefAction[] {
  const out: RefAction[] = []
  // An in-place kind is read in the drawer and has no page to open.
  if (card.open.type === 'route' && !inPlaceKinds.has(ref.kind)) out.push({ id: 'open', label: card.openLabel ?? 'Open page', type: 'open', target: card.open })
  else if (card.open.type === 'external') out.push({ id: 'open', label: 'Open link', type: 'open', target: card.open })
  out.push({ id: 'copy', label: card.open.type === 'copy' ? 'Copy path' : 'Copy ID', type: 'copy', text: card.open.type === 'copy' ? card.open.text : ref.canonical })
  for (const p of actionProviders.get(ref.kind) ?? []) {
    try {
      out.push(...p(ref, card))
    } catch {
      // A misbehaving provider must not break the card.
    }
  }
  return out
}

// ---------- drawer body ----------

export interface RefDetailProps {
  refId: RefIdentity
  card: PreviewCard
  /** Only for kinds that own their drawer chrome (see registerRefChrome). */
  onClose?: () => void
  expanded?: boolean
  onExpandedChange?: (expanded: boolean) => void
}

const ownChrome = new Set<string>()

/** The kind draws its own drawer header, actions and footer; the host drawer adds none. */
export function registerRefChrome(kind: string): () => void {
  ownChrome.add(kind)
  return () => {
    ownChrome.delete(kind)
  }
}
export const refOwnsChrome = (kind: string) => ownChrome.has(kind)

const detailRenderers = new Map<string, ComponentType<RefDetailProps>>()

/**
 * Replace the drawer body for a kind. The renderer is shown below the standard
 * header (icon, title, status) and above the standard actions; it receives the
 * resolved card and may fetch richer data itself.
 */
export function registerRefDetail(kind: string, component: ComponentType<RefDetailProps>): () => void {
  detailRenderers.set(kind, component)
  return () => {
    if (detailRenderers.get(kind) === component) detailRenderers.delete(kind)
  }
}
export function refDetail(kind: string): ComponentType<RefDetailProps> | undefined {
  return detailRenderers.get(kind)
}

// ---------- hovercard body ----------

const hoverRenderers = new Map<string, ComponentType<RefDetailProps>>()

/**
 * Replace the hovercard body for a kind, below the standard header. Shown only
 * while the card is open, so it may fetch.
 */
export function registerRefHover(kind: string, component: ComponentType<RefDetailProps>): () => void {
  hoverRenderers.set(kind, component)
  return () => {
    if (hoverRenderers.get(kind) === component) hoverRenderers.delete(kind)
  }
}
export function refHover(kind: string): ComponentType<RefDetailProps> | undefined {
  return hoverRenderers.get(kind)
}

// ---------- card header ----------

export interface RefCardProps {
  refId: RefIdentity
  card: PreviewCard
  children: (card: PreviewCard) => ReactNode
}

const cardEnrichers = new Map<string, ComponentType<RefCardProps>>()

/**
 * Let a kind correct the card header (title, status, updated) from fresher data
 * than the reference resolver holds, so header and body come from one snapshot.
 * The component calls `children` with the card to render.
 */
export function registerRefCard(kind: string, component: ComponentType<RefCardProps>): () => void {
  cardEnrichers.set(kind, component)
  return () => {
    if (cardEnrichers.get(kind) === component) cardEnrichers.delete(kind)
  }
}
export function refCardEnricher(kind: string): ComponentType<RefCardProps> | undefined {
  return cardEnrichers.get(kind)
}

// ---------- chip icon and own hover card ----------

const chipIcons = new Map<string, (ref: RefIdentity) => string>()

/** A kind may pick the chip's icon token from the reference itself (a file chip shows its type). */
export function registerRefChipIcon(kind: string, pick: (ref: RefIdentity) => string): () => void {
  chipIcons.set(kind, pick)
  return () => {
    if (chipIcons.get(kind) === pick) chipIcons.delete(kind)
  }
}
export const refChipIcon = (kind: string) => chipIcons.get(kind)

const ownHover = new Map<string, ComponentType<RefDetailProps>>()

/** The kind draws the whole hovercard body itself, with no standard header or footer. */
export function registerRefOwnHover(kind: string, component: ComponentType<RefDetailProps>): () => void {
  ownHover.set(kind, component)
  return () => {
    if (ownHover.get(kind) === component) ownHover.delete(kind)
  }
}
export const refOwnHover = (kind: string) => ownHover.get(kind)
