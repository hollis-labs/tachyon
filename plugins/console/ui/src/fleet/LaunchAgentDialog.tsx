import { useEffect, useRef, useState } from 'react'
import { FormDialog, InputCombobox } from '@hollis-labs/design-components'
import { AdminField as Field, AdminInput, AdminSelect, AdminTextarea } from '@hollis-labs/kit-admin'
import { launchAgent, launchOptions, LaunchRequestError, type LaunchAttempt, type LaunchProfile, type LaunchWorker } from './launch-client'

export function LaunchAgentDialog({ open, onClose, onLaunched }: { open: boolean; onClose: () => void; onLaunched: (id: string) => void }) {
  const [profiles, setProfiles] = useState<LaunchProfile[]>([])
  const [workers, setWorkers] = useState<LaunchWorker[]>([])
  const [loading, setLoading] = useState(true)
  const [runtime, setRuntime] = useState('')
  const [profileId, setProfileId] = useState<string | null>(null)
  const [worker, setWorker] = useState('')
  const [prompt, setPrompt] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [success, setSuccess] = useState(false)
  const [locked, setLocked] = useState(false)
  const [reload, setReload] = useState(0)
  const attempt = useRef<LaunchAttempt | null>(null)
  // Clearing the form must not reuse its fields or error. Retain only recovery
  // identities so submitting the same request again cannot duplicate a session.
  const attempts = useRef(new Map<string, LaunchAttempt>())
  const inFlight = useRef(false)
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const profile = profiles.find(p => p.id === profileId)
  function reset() {
    clearTimeout(closeTimer.current)
    setSuccess(false); setLocked(false); setError(''); attempt.current = null
    setProfileId(null); setWorker(''); setPrompt(''); setRuntime('')
  }
  function close() { reset(); onClose() }
  function changed() { setError(''); setLocked(false); attempt.current = null }
  useEffect(() => () => clearTimeout(closeTimer.current), [])
  useEffect(() => {
    if (!open) return
    let active = true
    setLoading(true)
    launchOptions().then(options => { if (active) { setProfiles(options.profiles); setWorkers(options.workers) } })
      .catch(e => { if (active) setError(e instanceof Error ? e.message : 'Could not load launch options.') })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [open, reload])
  async function submit() {
    if (inFlight.current || success || (!attempt.current && (!profile || loading))) return
    inFlight.current = true
    setBusy(true); setError(''); setLocked(true)
    const input = { launch: profile!.id, ...(worker ? { worker } : {}), ...(prompt.trim() ? { prompt_append: prompt } : {}) }
    const requestKey = JSON.stringify(input)
    attempt.current = attempts.current.get(requestKey) ?? { input: { ...input, idempotency_key: `tachyon-launch-${crypto.randomUUID()}` } }
    attempts.current.set(requestKey, attempt.current)
    try {
      const id = await launchAgent(attempt.current)
      attempts.current.delete(requestKey)
      setSuccess(true)
      onLaunched(id)
      closeTimer.current = setTimeout(() => {
        close()
      }, 1000)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Launch failed. Retry to recover this request.')
      // A definite pre-create validation/auth refusal can be corrected. Unknown
      // outcomes and any already-created session keep their recovery identity.
      if (!attempt.current?.sessionId && e instanceof LaunchRequestError && [400, 401, 403, 404, 422].includes(e.status)) {
        attempts.current.delete(requestKey); attempt.current = null; setLocked(false)
      }
      // An error must be visible even when the prompt was scrolled into view.
      requestAnimationFrame(() => document.querySelector('[data-launch-error]')?.scrollIntoView({ block: 'nearest' }))
    } finally { inFlight.current = false; setBusy(false) }
  }
  return <FormDialog open={open} onClose={close} title="Launch agent" description="Start an agent from a saved launch profile." onSubmit={submit} submitOnEnter={false}
    submitting={busy || success} submitDisabled={loading || (!profile && !attempt.current)} submitLabel={success ? 'Launched' : busy ? 'Launching…' : locked ? 'Retry launch' : 'Launch'} className="rounded-none">
    {success ? <div role="status" className="flex h-full items-center justify-center dash-body text-success">Agent launched.</div> : <>
      {error && <div role="alert" data-launch-error className="dash-body text-danger">{error} <button type="button" className="ml-2 underline" disabled={busy} onClick={reset}>Clear form</button>{!locked && <button type="button" className="ml-2 underline" onClick={() => { setError(''); setReload(n => n + 1) }}>Reload options</button>}</div>}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="Runtime" htmlFor="launch-runtime"><AdminSelect id="launch-runtime" className="w-full" disabled={loading || busy} value={runtime} onChange={e => { changed(); setRuntime(e.target.value); setProfileId(null); setWorker('') }}>
          <option value="">All runtimes</option>{[...new Set(profiles.map(p => p.provider))].sort().map(r => <option key={r} value={r}>{r}</option>)}
        </AdminSelect></Field>
        <Field label="Machine" htmlFor="launch-machine"><AdminSelect id="launch-machine" className="w-full" disabled={loading || busy} value={worker} onChange={e => { changed(); setWorker(e.target.value) }}>
          <option value="">{profile?.worker ? `Profile default · ${profile.worker}` : 'Profile default · hub'}</option>
          {workers.map(w => <option key={w.id} value={w.id} disabled={!w.connected}>{w.label || w.id}{!w.connected ? ' · offline' : ''}</option>)}
        </AdminSelect></Field>
      </div>
      <InputCombobox label="Launch profile" placeholder="Search profiles, agents, projects…" value={profileId} disabled={busy} loading={loading}
        onChange={id => { changed(); setProfileId(id); setWorker('') }} items={profiles.filter(p => !runtime || p.provider === runtime).map(p => ({ id: p.id, label: p.display_name || p.id, description: `${p.provider} · ${p.project} · ${p.agent}`, keywords: [p.id, p.agent, p.project, p.provider] }))} />
      <Field label="Label" htmlFor="launch-label"><AdminInput id="launch-label" className="w-full" readOnly value={profile ? profile.display_name || profile.agent : ''} placeholder="From the selected profile" />
        <p className="dash-support text-fg-muted">Uses the saved profile’s label. Custom session labels need Tether support.</p>
      </Field>
      <Field label="Boot prompt (optional)" htmlFor="launch-prompt"><AdminTextarea id="launch-prompt" className="min-h-24 w-full" disabled={busy} value={prompt} onChange={e => { changed(); setPrompt(e.target.value) }} placeholder="What should this agent work on?" maxLength={32000} />
        <p className="dash-support text-fg-muted">Added to the profile’s existing instructions.</p>
      </Field>
      {locked && <p className="dash-support text-fg-muted">Retry recovers this launch without creating another agent.{attempt.current?.sessionId ? ` Session: ${attempt.current.sessionId}` : ''}</p>}
    </>}
  </FormDialog>
}
