import { AlertTriangle, RefreshCw } from 'lucide-react'
import { MetaList, Skeleton, type MetaItem } from '@hollis-labs/design-components'
import { ago } from '@console/lib/agent-activity'
import { MessageBody } from '../chat/MessageBody'
import { TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND } from '../refs'
import { registerRefCard, registerRefDetail, registerRefHover, type RefCardProps, type RefDetailProps } from './extensions'
import { tesseractEntity, tesseractItemType } from './tesseractResolver'
import { useTesseractItem } from './tesseractDetail'

const LABEL = 'text-caption uppercase tracking-label text-fg-faint'
const CHIP = 'inline-flex items-center border border-border px-1.5 py-px font-mono text-caption text-fg-secondary'
const BTN = 'inline-flex items-center gap-1.5 border border-border px-2 py-1 font-mono text-caption uppercase tracking-label text-fg-secondary transition-colors hover:border-fg-faint hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

function stamp(iso: string | undefined, now: number): string {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isFinite(t) ? ago(t, now) : '-'
}
function exact(iso: string | undefined): string | undefined {
  const t = iso ? Date.parse(iso) : NaN
  return Number.isFinite(t) ? new Date(t).toLocaleString() : undefined
}

/** Drawer body for a Tesseract item: the body as markdown (ids inside it link too), metadata and tags, read-only. */
export function TesseractDrawerPane({ refId }: RefDetailProps) {
  const { entry, refresh, available } = useTesseractItem(refId.kind, refId.canonical)
  const now = Date.now()
  if (!available) return <p className="text-control text-fg-faint">Tesseract details are not available here.</p>
  if (!entry || (entry.phase === 'loading' && !entry.item)) {
    return (
      <div className="space-y-2" aria-label="Loading item" aria-busy="true">
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-5/6" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    )
  }
  if (!entry.item) {
    return entry.phase === 'not-found' ? (
      <p className="text-control text-fg-faint">Tesseract has no such item.</p>
    ) : (
      <div className="flex items-start gap-2 text-control text-destructive" role="alert">
        <AlertTriangle className="mt-px h-3.5 w-3.5 shrink-0" />
        <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">Could not read Tesseract{entry.error ? `: ${entry.error}` : ''}</span>
        <button type="button" className={BTN} onClick={refresh}><RefreshCw className="h-3 w-3" />Retry</button>
      </div>
    )
  }
  const item = entry.item
  const summary = item.payload?.summary
  const body = item.payload?.body
  const meta: MetaItem[] = [
    { label: 'Type', value: tesseractItemType(item) },
    { label: 'Domain', value: item.domain },
    { label: 'Namespace', value: <span className="font-mono [overflow-wrap:anywhere]">{item.namespace}</span> },
    { label: 'Key', value: <span className="font-mono [overflow-wrap:anywhere]">{item.key}</span> },
    { label: 'Item ID', value: <span className="font-mono">{item.item_id}</span> },
    { label: 'Updated', value: <time dateTime={item.updated_at} title={exact(item.updated_at)}>{stamp(item.updated_at, now)}</time> },
    { label: 'Created', value: <time dateTime={item.created_at} title={exact(item.created_at)}>{stamp(item.created_at, now)}</time> },
  ]
  if (item.author?.agent_id) meta.push({ label: 'Author', value: <span className="font-mono [overflow-wrap:anywhere]">{item.author.agent_id}</span> })
  return (
    <div className="flex min-w-0 flex-col gap-4" data-testid="tesseract-detail">
      {summary && (
        <section aria-label="Summary">
          <h3 className={LABEL}>Summary</h3>
          <p className="mt-1.5 text-control leading-relaxed text-fg [overflow-wrap:anywhere]">{summary}</p>
        </section>
      )}
      <section aria-label="Content" className={summary ? 'border-t border-border pt-3' : ''}>
        <h3 className={LABEL}>Content</h3>
        {body ? <MessageBody body={body} /> : <p className="mt-1.5 text-control text-fg-faint">No body.</p>}
      </section>
      <section aria-label="Details" className="border-t border-border pt-3">
        <MetaList items={meta} columns={2} />
        {item.tags && item.tags.length > 0 && (
          <div className="mt-3 flex flex-wrap gap-1.5" aria-label="Tags">
            {item.tags.map((t) => <span key={t} className={CHIP}>{t}</span>)}
          </div>
        )}
      </section>
      <div className="flex items-center gap-3 border-t border-border pt-3">
        <button type="button" className={BTN} onClick={refresh} disabled={entry.phase === 'loading'}>
          <RefreshCw className={`h-3 w-3 ${entry.phase === 'loading' ? 'animate-spin' : ''}`} />
          Refresh
        </button>
        <span className="font-mono text-caption text-fg-faint">fetched {stamp(new Date(entry.fetchedAt).toISOString(), now)}</span>
        {entry.phase === 'error' && <span className="text-caption text-destructive" role="alert">Refresh failed; showing the last copy.</span>}
      </div>
    </div>
  )
}

/** Hovercard body: the summary and the few facts worth a glance (domain is in the header). */
export function TesseractHoverMeta({ refId }: RefDetailProps) {
  const { entry } = useTesseractItem(refId.kind, refId.canonical)
  const item = entry?.item
  const now = Date.now()
  if (!item) {
    return entry?.phase === 'error'
      ? <p className="mt-3 text-control text-fg-faint">Details unavailable right now.</p>
      : <div className="mt-3 space-y-1.5" aria-hidden><Skeleton className="h-2 w-3/4" /><Skeleton className="h-2 w-1/2" /></div>
  }
  return (
    <dl className="mt-3 grid grid-cols-2 gap-x-3 border-t border-border pt-3" data-testid="tesseract-hover-meta">
      <div className="min-w-0">
        <dt className={LABEL}>Type</dt>
        <dd className="mt-0.5 truncate text-control text-fg">{tesseractItemType(item)}</dd>
      </div>
      <div className="min-w-0">
        <dt className={LABEL}>Updated</dt>
        <dd className="mt-0.5 text-control text-fg" title={exact(item.updated_at)}>{stamp(item.updated_at, now)}</dd>
      </div>
    </dl>
  )
}

/** Header from the same snapshot as the body, so a refresh cannot leave an old title beside new content. */
export function TesseractCard({ refId, card, children }: RefCardProps) {
  const { entry } = useTesseractItem(refId.kind, refId.canonical)
  const item = entry?.item
  if (!item) return <>{children(card)}</>
  const e = tesseractEntity(item)
  return <>{children({ ...card, title: e.title, subtitle: e.subtitle, description: e.description, status: e.status, updatedAt: e.updatedAt })}</>
}

let registered = false
/** Idempotent: gives tesseract.item and tesseract.key the rich drawer, hovercard and header the task has. */
export function registerTesseractRefs(): void {
  if (registered) return
  registered = true
  for (const kind of [TESSERACT_ITEM_KIND, TESSERACT_KEY_KIND]) {
    registerRefDetail(kind, TesseractDrawerPane)
    registerRefHover(kind, TesseractHoverMeta)
    registerRefCard(kind, TesseractCard)
  }
}
