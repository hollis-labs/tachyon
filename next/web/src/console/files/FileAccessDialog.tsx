import { Button, Dialog, DialogContent, DialogDescription, DialogTitle, Input, Skeleton } from '@hollis-labs/design-components'
import { useEffect, useState } from 'react'
import { errorMessage, type FileSettings, fileApi, formatSize, type Roots } from './api'

type Load = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; roots: Roots; settings: FileSettings }

const LABEL = 'font-mono text-caption uppercase tracking-label text-fg-muted'
const casterRoot = (ns: string) => `caster://${ns}/*`

/** App roots work today; the per-user section waits for backend F-V4 and is not shown. */
export function FileAccessDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const [load, setLoad] = useState<Load>({ status: 'loading' })
  const [roots, setRoots] = useState<string[]>([])
  const [spaces, setSpaces] = useState<string[]>([])
  const [adding, setAdding] = useState(false)
  const [entry, setEntry] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    let live = true
    setLoad({ status: 'loading' })
    setError('')
    setAdding(false)
    Promise.all([fileApi.roots(), fileApi.settings()]).then(
      ([r, s]) => {
        if (!live) return
        setRoots(s.app.roots)
        setSpaces(s.app.caster_namespaces)
        setLoad({ status: 'ready', roots: r, settings: s })
      },
      (e) => live && setLoad({ status: 'error', message: errorMessage(e) }),
    )
    return () => {
      live = false
    }
  }, [open])

  const add = () => {
    const v = entry.trim()
    if (!v) return
    const m = /^caster:\/\/([A-Za-z0-9._-]+)\/\*$/.exec(v)
    if (m) setSpaces((s) => (s.includes(m[1]) ? s : [...s, m[1]]))
    else setRoots((r) => (r.includes(v) ? r : [...r, v]))
    setEntry('')
    setAdding(false)
  }

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      await fileApi.putSettings({ roots, caster_namespaces: spaces })
      onOpenChange(false)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setSaving(false)
    }
  }

  const fixed = load.status === 'ready' ? load.roots.roots.filter((r) => r.source === 'default') : []
  const caps = load.status === 'ready' ? load.roots.caps : null

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="gap-3 sm:max-w-xl">
        <DialogTitle className="font-sans text-base font-bold normal-case tracking-normal">File access</DialogTitle>
        <DialogDescription>Folders Tachyon may show in the viewer. App settings apply to everyone on this install.</DialogDescription>
        {load.status === 'loading' && <Skeleton className="h-40 w-full" />}
        {load.status === 'error' && (
          <p role="alert" className="text-control text-fg-secondary">
            {load.message}
          </p>
        )}
        {load.status === 'ready' && (
          <>
            <section aria-label="App folders" className="space-y-1 rounded-panel border border-border p-3">
              <h3 className={LABEL}>App</h3>
              <ul className="space-y-1">
                {fixed.map((r) => (
                  <li key={r.id} className="flex items-center gap-2 font-mono text-control text-fg">
                    <span className="min-w-0 flex-1 truncate">{r.path_display}</span>
                    <span className="rounded-xs border border-border px-1.5 font-mono text-caption text-fg-secondary">read</span>
                    <span aria-hidden className="size-4" />
                  </li>
                ))}
                {roots.map((r) => (
                  <Row key={r} text={r} onRemove={() => setRoots((x) => x.filter((v) => v !== r))} />
                ))}
                {spaces.map((n) => (
                  <Row key={n} text={casterRoot(n)} onRemove={() => setSpaces((x) => x.filter((v) => v !== n))} />
                ))}
              </ul>
              {adding ? (
                <form
                  className="flex gap-2 pt-1"
                  onSubmit={(e) => {
                    e.preventDefault()
                    add()
                  }}
                >
                  <Input aria-label="Folder or caster namespace" autoFocus value={entry} onChange={(e) => setEntry(e.target.value)} placeholder="/path/to/folder or caster://name/*" className="h-8 flex-1 font-mono" />
                  <Button type="submit" size="sm" variant="outline">
                    Add
                  </Button>
                </form>
              ) : (
                <Button type="button" size="sm" variant="ghost" onClick={() => setAdding(true)}>
                  + Add folder or caster namespace
                </Button>
              )}
            </section>
            <section aria-label="Limits" className="space-y-1 rounded-panel border border-border p-3">
              <h3 className={LABEL}>Limits</h3>
              <dl className="space-y-1 text-control">
                <Limit k="Text and code" v={caps ? formatSize(caps.text_bytes) : ''} />
                <Limit k="Images" v={caps ? formatSize(caps.image_bytes) : ''} />
                <Limit k="Download" v="Allowed" />
                <Limit k="Protected patterns" v=".env · keys · credentials (always on)" />
              </dl>
            </section>
          </>
        )}
        {error && (
          <p role="alert" className="text-control text-danger">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button type="button" size="sm" disabled={saving || load.status !== 'ready'} onClick={() => void save()}>
            {saving ? 'Saving' : 'Save'}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function Row({ text, onRemove }: { text: string; onRemove: () => void }) {
  return (
    <li className="flex items-center gap-2 font-mono text-control text-fg">
      <span className="min-w-0 flex-1 truncate">{text}</span>
      <span className="rounded-xs border border-border px-1.5 font-mono text-caption text-fg-secondary">read</span>
      <button type="button" aria-label={`Remove ${text}`} onClick={onRemove} className="size-4 text-fg-muted hover:text-fg">
        ×
      </button>
    </li>
  )
}

function Limit({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex justify-between gap-4">
      <dt className="text-fg-secondary">{k}</dt>
      <dd className="font-bold text-fg">{v}</dd>
    </div>
  )
}
