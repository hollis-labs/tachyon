import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@hollis-labs/design-components'
import { AdminField, AdminInput } from '@hollis-labs/kit-admin'
import { Check, Copy, Download, Shuffle } from 'lucide-react'
import { useMemo, useState } from 'react'
import { AVATAR_BASE, DEFAULT_FILL, PALETTE, avatarClient, avatarName, previewUrl, type AvatarClient, type AvatarColors, type AvatarKnobs } from './avatar-service'
import { AgentAvatar } from './AgentAvatar'
import { Btn, Chip } from './ui'

const ACCENT = 'border-warning/60 bg-warning/10 text-warning'
const pick = <T,>(items: readonly T[]) => items[Math.floor(Math.random() * items.length)]
const randomSeed = () => Math.random().toString(36).slice(2, 10)
const iconBtn = 'inline-flex size-8 items-center justify-center border border-border text-fg-muted transition-colors hover:text-fg focus-visible:bg-panel-2 focus-visible:outline-none disabled:opacity-40'

/** Widget for choosing an agent's avatar. The face comes from the URN; the knobs change colours and fill, and only those can be saved. */
export function AvatarStudio({ urn, name, open, onClose, client = avatarClient }: { urn: string; name: string; open: boolean; onClose: () => void; client?: AvatarClient }) {
  const [seed, setSeed] = useState(urn)
  const [fill, setFill] = useState(DEFAULT_FILL)
  const [colors, setColors] = useState<AvatarColors>({ kind: 'seed' })
  const [busy, setBusy] = useState(false)
  const [status, setStatus] = useState<{ tone: 'ok' | 'error'; text: string }>()
  const [copied, setCopied] = useState(false)

  const knobs: AvatarKnobs = { seed, fill, colors }
  const exploring = seed.trim() !== urn
  const url = useMemo(() => previewUrl({ seed: seed || urn, fill, colors }), [seed, urn, fill, colors])
  const pngUrl = previewUrl({ seed: seed || urn, fill, colors }, 'png', 512)
  const custom = colors.kind === 'custom' ? colors : { kind: 'custom' as const, accent: PALETTE[1].accent, bg: PALETTE[1].bg }

  const random = () => {
    setColors({ kind: 'slot', slot: pick(PALETTE).slot })
    setFill(Math.round(30 + Math.random() * 60))
    setStatus(undefined)
  }
  const run = async (action: () => ReturnType<AvatarClient['save']>, done: string) => {
    setBusy(true); setStatus(undefined)
    const result = await action()
    setBusy(false)
    setStatus(result.ok ? { tone: 'ok', text: done } : { tone: 'error', text: result.message })
  }
  const copy = async () => {
    try {
      const blob = await (await fetch(pngUrl)).blob()
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })])
      setStatus({ tone: 'ok', text: 'Image copied.' })
    } catch {
      // Cross-origin fetches are only allowed from the production origin; the URL is the fallback.
      try { await navigator.clipboard.writeText(pngUrl); setStatus({ tone: 'ok', text: 'Image link copied.' }) } catch { setStatus({ tone: 'error', text: 'Copy is not available in this browser.' }); return }
    }
    setCopied(true); setTimeout(() => setCopied(false), 1500)
  }
  const download = async () => {
    const file = `${avatarName(seed || urn)}.png`
    try {
      const blob = await (await fetch(pngUrl)).blob()
      const a = Object.assign(document.createElement('a'), { href: URL.createObjectURL(blob), download: file })
      a.click(); URL.revokeObjectURL(a.href)
    } catch { window.open(pngUrl, '_blank', 'noopener') }
  }

  return <Dialog open={open} onOpenChange={next => { if (!next) onClose() }}>
    <DialogContent widthClassName="max-w-2xl">
      {/* The dialog renders in a portal outside the page's yellow accent scope, so it is restored here. */}
      <div data-dashboard-accent="yellow">
      <DialogHeader>
        <DialogTitle>Avatar · {name}</DialogTitle>
        <DialogDescription>Previews are free and store nothing. Set avatar replaces the saved image for this agent.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-5 px-5 py-4 sm:grid-cols-[14rem_1fr]">
        <div className="space-y-3">
          <div className="flex aspect-square items-center justify-center border border-border bg-panel-2">
            <img src={url} alt={`Preview of ${name}'s avatar`} className="size-full object-contain [image-rendering:pixelated]" />
          </div>
          <div className="flex items-center gap-3">
            <img src={url} alt="" className="dash-identity-definition [image-rendering:pixelated]" /><img src={url} alt="" className="size-5 [image-rendering:pixelated]" />
            <span className="ml-auto flex gap-1.5">
              <button type="button" className={iconBtn} onClick={() => void copy()} aria-label="Copy image">{copied ? <Check className="size-4" /> : <Copy className="size-4" />}</button>
              <button type="button" className={iconBtn} onClick={() => void download()} aria-label="Download image"><Download className="size-4" /></button>
            </span>
          </div>
          <p className="break-all font-mono dash-support text-fg-faint">{AVATAR_BASE}/avatars/{avatarName(urn)}.svg</p>
        </div>
        <div className="min-w-0 space-y-4">
          <AdminField label="Colours">
            <div role="group" aria-label="Palette" className="grid grid-cols-8 gap-1.5">
              <button type="button" aria-pressed={colors.kind === 'seed'} title="Agent's own colours" onClick={() => setColors({ kind: 'seed' })}
                className={`flex aspect-square items-center justify-center border font-mono dash-caption ${colors.kind === 'seed' ? ACCENT : 'border-border text-fg-faint'}`}>auto</button>
              {PALETTE.map(p => <button key={p.slot} type="button" title={p.slot} aria-label={p.slot} aria-pressed={colors.kind === 'slot' && colors.slot === p.slot}
                onClick={() => setColors({ kind: 'slot', slot: p.slot })} style={{ background: p.bg }}
                className={`aspect-square border-2 p-1 ${colors.kind === 'slot' && colors.slot === p.slot ? 'border-warning' : 'border-transparent hover:border-border'}`}><span className="block size-full" style={{ background: p.accent }} /></button>)}
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-3">
              <label className="flex items-center gap-2 dash-support text-fg-muted">Accent<input type="color" aria-label="Accent colour" value={custom.accent} onChange={e => setColors({ ...custom, accent: e.target.value })} /></label>
              <label className="flex items-center gap-2 dash-support text-fg-muted">Background<input type="color" aria-label="Background colour" value={custom.bg} onChange={e => setColors({ ...custom, bg: e.target.value })} /></label>
              {colors.kind === 'custom' && <Chip tone={ACCENT}>custom</Chip>}
            </div>
          </AdminField>
          <AdminField label={`Fill (${fill}%)`} htmlFor="avatar-fill">
            <input id="avatar-fill" type="range" min={0} max={100} value={fill} onChange={e => setFill(Number(e.target.value))} className="w-full accent-[var(--color-warning)]" />
          </AdminField>
          <AdminField label="Explore a different face" htmlFor="avatar-seed">
            <div className="flex gap-2">
              <AdminInput id="avatar-seed" value={seed} onChange={e => setSeed(e.target.value)} className="min-w-0 flex-1 font-mono" />
              <button type="button" className={iconBtn} onClick={() => setSeed(randomSeed())} aria-label="Random face"><Shuffle className="size-4" /></button>
            </div>
            {exploring && <p className="mt-1 dash-support text-warning">Preview only. A saved avatar always uses this agent's URN, so only colours and fill can be set. <button type="button" className="underline" onClick={() => setSeed(urn)}>Back to the agent's face</button></p>}
          </AdminField>
        </div>
      </div>
      {status && <p role={status.tone === 'error' ? 'alert' : 'status'} className={`border-t border-border/80 px-5 py-2 dash-body ${status.tone === 'error' ? 'text-danger' : 'text-fg'}`}>{status.text}</p>}
      <div className="flex flex-wrap items-center gap-2 border-t border-border px-5 py-3">
        <Btn disabled={busy} onClick={random}>Random</Btn>
        <Btn disabled={busy} onClick={() => void run(() => client.clear(urn), 'Avatar cleared. The agent shows its default until a new one is set.')}>Clear</Btn>
        <span className="ml-auto flex gap-2">
          <Btn disabled={busy} onClick={onClose}>Close</Btn>
          <Btn tone="primary" disabled={busy || exploring} onClick={() => void run(() => client.save(urn, knobs), 'Avatar set.')}>Set avatar</Btn>
        </span>
      </div>
      </div>
    </DialogContent>
  </Dialog>
}

/** Avatar thumbnail with the button that opens the studio; the studio mounts only while open so it always starts from the agent's own face. */
export function AvatarControl({ urn, name }: { urn: string; name: string }) {
  const [open, setOpen] = useState(false)
  return <div className="flex items-center gap-3">
    <AgentAvatar identity={urn} size="large" />
    <Btn onClick={() => setOpen(true)}>Edit avatar</Btn>
    {open && <AvatarStudio urn={urn} name={name} open onClose={() => setOpen(false)} />}
  </div>
}
