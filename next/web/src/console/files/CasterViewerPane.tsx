import { Button, Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Skeleton } from '@hollis-labs/design-components'
import { Check, Copy, RefreshCw } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { RefDetailProps } from '../autolink/extensions'
import { relativeTime } from '../builder/changes'
import { type CasterInfo, casterStat, errorMessage, fileApi, isCasterRef, pinCasterRef } from './api'
import { Body } from './FileViewerPane'

const BTN =
  'inline-flex items-center gap-1.5 border border-border px-2 py-1 text-control text-fg-secondary transition-colors hover:border-fg-faint hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50'

type Load = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; info: CasterInfo }

/** Drawer body for a caster:// artifact: resolve first, then the shared viewer body at a pinned revision. */
export function CasterViewerPane({ refId }: RefDetailProps) {
  const ref = refId.canonical
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [tick, setTick] = useState(0)
  const [rev, setRev] = useState<number | null>(null)
  const [copied, setCopied] = useState(false)

  // biome-ignore lint/correctness/useExhaustiveDependencies: tick re-resolves on Reload
  useEffect(() => {
    if (!isCasterRef(ref)) return
    let live = true
    setLoad({ status: 'loading' })
    fileApi.resolve(ref).then(
      (info) => {
        if (!live) return
        setLoad({ status: 'ready', info })
        setRev((r) => r ?? info.rev)
      },
      (e) => live && setLoad({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [ref, tick])

  const copy = () => {
    void navigator.clipboard?.writeText(ref)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <section aria-label={`Artifact ${ref.replace('caster://', '')}`} className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <button type="button" className={BTN} onClick={() => setTick((n) => n + 1)}>
          <RefreshCw className="h-3 w-3" aria-hidden /> Reload
        </button>
        <button type="button" className={BTN} onClick={copy}>
          {copied ? <Check className="h-3 w-3" aria-hidden /> : <Copy className="h-3 w-3" aria-hidden />} {copied ? 'Copied' : 'Copy ref'}
        </button>
      </div>
      {load.status === 'loading' && <Skeleton className="h-40 w-full" />}
      {load.status === 'error' && (
        <p role="alert" className="text-control text-fg-secondary">
          {load.message}
        </p>
      )}
      {load.status === 'ready' && rev !== null && <Ready info={load.info} rev={rev} onRev={setRev} tick={tick} />}
      <p className="break-all font-mono text-caption text-fg-faint">{ref}</p>
    </section>
  )
}

function Ready({ info, rev, onRev, tick }: { info: CasterInfo; rev: number; onRev: (r: number) => void; tick: number }) {
  const pinned = pinCasterRef(info.ref, rev)
  const current = info.revisions.find((r) => r.rev === rev)
  return (
    <>
      <div className="space-y-1">
        <h3 className="text-control font-medium text-fg">{current?.title || info.title || info.name}</h3>
        <div className="flex items-center gap-2 text-caption text-fg-muted">
          <span id="rev-label">Revision</span>
          <Select value={String(rev)} onValueChange={(v) => v && onRev(Number(v))}>
            <SelectTrigger aria-labelledby="rev-label" className="h-8 min-w-48 text-control">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {info.revisions.map((r) => (
                <SelectItem key={r.rev} value={String(r.rev)}>
                  {`Rev ${r.rev}${r.rev === info.latest_rev ? ' (latest)' : ''}${r.at ? ` · ${relativeTime(r.at)}` : ''}`}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {info.floating && <p className="text-caption text-fg-muted">This link follows the latest revision; showing rev {info.rev} when it was opened.</p>}
        {rev !== info.latest_rev && (
          <p className="text-caption text-fg-muted">
            An older revision. Latest is rev {info.latest_rev}.{' '}
            <Button type="button" variant="link" size="xs" onClick={() => onRev(info.latest_rev)}>
              Go to latest
            </Button>
          </p>
        )}
      </div>
      <Body stat={casterStat(info, pinned)} path={pinned} tick={tick} />
    </>
  )
}
