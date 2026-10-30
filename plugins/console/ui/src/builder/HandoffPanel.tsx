import { Button } from '@hollis-labs/design-components'
import { Download, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { BuilderError, type Change, builderApi, errorMessage } from './api'
import { Diff } from './ReviewDialog'

const COOLDOWN_MS = 5000

/** What the operator is waiting on, in one live region. */
export function handoffStatus(c: Change): { text: string; lines: string[]; danger?: boolean } {
  switch (c.state) {
    case 'awaiting_handoff':
      return { text: 'Waiting for you to plan, approve and apply in Tether.', lines: [] }
    case 'awaiting_approval':
      return { text: c.plan?.plan_id ? `Plan ${c.plan.plan_id.slice(0, 8)} is waiting for approval in Tether.` : 'The plan is waiting for approval in Tether.', lines: [] }
    case 'applied': {
      const n = c.handoff?.files ?? 0
      const lines = ['Running sessions keep their old configuration; new launches use the update.']
      if (c.effect?.restart_required?.length) lines.push(`Restart required: ${c.effect.restart_required.join(', ')}.`)
      return { text: n ? `Applied. Detected ${n} file${n === 1 ? '' : 's'} changed.` : 'Applied.', lines }
    }
    case 'stale':
      return { text: 'Changed elsewhere. Review before preparing again.', lines: [] }
    case 'failed':
      return { text: `Failed: ${c.failure?.step}: ${c.failure?.message}.`, lines: [], danger: true }
    case 'cancelled':
      return { text: 'Change cancelled.', lines: [] }
    default:
      return { text: '', lines: [] }
  }
}

/** The hand-off panel: bundle, three commands, status. Direct mode never renders this. */
export function HandoffPanel({ change, onChange, onReload }: { change: Change; onChange: (c: Change) => void; onReload: () => void }) {
  const [copied, setCopied] = useState('')
  const [error, setError] = useState('')
  const [checking, setChecking] = useState(false)
  const [cooling, setCooling] = useState(false)
  const timers = useRef<number[]>([])
  useEffect(() => () => timers.current.forEach(clearTimeout), [])

  const flash = (key: string) => {
    setCopied(key)
    timers.current.push(window.setTimeout(() => setCopied(''), 1500))
  }
  const copy = async (key: string, text: string) => {
    try {
      await navigator.clipboard?.writeText(text)
      flash(key)
    } catch {
      setError('Could not copy. Select the text and copy it by hand.')
    }
  }
  // The bundle holds full file content: fetched at the click, handed to the clipboard or a file, not kept or logged.
  const withBundle = async (key: string, use: (json: string) => void | Promise<void>) => {
    setError('')
    try {
      const json = JSON.stringify(await builderApi.bundle(change.change_id))
      await use(json)
      flash(key)
    } catch (e) {
      setError(errorMessage(e))
    }
  }
  const download = (json: string) => {
    const url = URL.createObjectURL(new Blob([json], { type: 'application/json' }))
    const a = document.createElement('a')
    a.href = url
    a.download = 'bundle.json'
    a.click()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  const checkNow = async () => {
    setChecking(true)
    setError('')
    try {
      // The check route is optional: where it is missing, Check now just reads the change again.
      onChange(
        await builderApi.check(change.change_id).catch((e) => {
          if (e instanceof BuilderError && (e.status === 404 || e.status === 405)) return builderApi.change(change.change_id)
          throw e
        }),
      )
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setChecking(false)
      setCooling(true)
      timers.current.push(window.setTimeout(() => setCooling(false), COOLDOWN_MS))
    }
  }

  const active = change.state === 'awaiting_handoff' || change.state === 'awaiting_approval'
  const status = handoffStatus(change)
  const commands = change.handoff?.commands ?? []

  return (
    <div className="space-y-3">
      {active && (
        <>
          <div className="space-y-1">
            <div className="flex flex-wrap items-center gap-2">
              <Button type="button" variant="outline" size="sm" className="max-sm:min-h-10" onClick={() => withBundle('bundle', (j) => navigator.clipboard?.writeText(j))}>
                {copied === 'bundle' ? 'Copied' : 'Copy bundle'}
              </Button>
              <Button type="button" variant="outline" size="sm" className="max-sm:min-h-10" onClick={() => withBundle('download', download)}>
                <Download className="h-3 w-3" /> {copied === 'download' ? 'Downloaded' : 'Download bundle'}
              </Button>
            </div>
            <p className="dash-support text-fg-muted">The bundle holds the full file content. It stays in your browser; do not paste it into chat or tickets.</p>
          </div>
          {commands.length > 0 ? (
            <div className="space-y-2">
              <ol className="space-y-2">
                {commands.map((c, i) => {
                  // An action is a tool a Tether session runs (no CLI); a shell step is a line to run on the host.
                  const action = c.kind === 'action'
                  const shell = !action && (c.kind === undefined || c.kind === 'shell')
                  const envBlocked = action && change.handoff?.session_available === false && change.handoff.reason === 'contains_env_values'
                  const tag = action ? 'Run in a Tether session' : shell ? 'Run in a terminal' : ''
                  return (
                    <li key={c.step} className="space-y-1">
                      <p className="dash-support font-medium text-fg">
                        {i + 1}. {c.label}
                        {tag && <span className="ml-2 font-normal text-fg-muted">{tag}</span>}
                      </p>
                      {action &&
                        (envBlocked ? (
                          <p className="dash-support text-fg-muted">This change includes environment values. Do not paste the bundle into a session or chat. Use a local Tether session that reads the downloaded file.</p>
                        ) : (
                          <>
                            {c.tool && (
                              <p className="dash-support text-fg-secondary">
                                Tool: <span className="font-mono">{c.tool}</span>
                              </p>
                            )}
                            {c.step === 'plan' && <p className="dash-support text-fg-muted">Give the session the bundle (Copy bundle above).</p>}
                          </>
                        ))}
                      {!action && c.command && (
                        <div className="flex items-start gap-2">
                          <code aria-label={`${c.step[0].toUpperCase()}${c.step.slice(1)} command`} className="block min-w-0 flex-1 whitespace-pre-wrap break-all bg-surface p-2 font-mono dash-support text-fg">
                            {c.command}
                          </code>
                          <Button type="button" variant="outline" size="sm" className="shrink-0 max-sm:min-h-10" onClick={() => copy(c.step, c.command ?? '')}>
                            {copied === c.step ? 'Copied' : 'Copy'}
                          </Button>
                        </div>
                      )}
                    </li>
                  )
                })}
              </ol>
              <p className="dash-support text-fg-muted">Approval needs your passphrase.</p>
            </div>
          ) : (
            <p className="dash-support text-fg-muted">The commands are not available yet. Copy or download the bundle and plan it in Tether.</p>
          )}
        </>
      )}
      {change.state === 'stale' && (
        <div className="space-y-2">
          {change.stale?.theirs_diff && <Diff text={change.stale.theirs_diff} />}
          <Button type="button" variant="outline" size="sm" className="max-sm:min-h-10" onClick={onReload}>
            Reload theirs
          </Button>
        </div>
      )}
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <div role="status" className={`dash-body ${status.danger ? 'text-danger' : 'text-fg'}`}>
          <p>{status.text}</p>
          {status.lines.map((l) => (
            <p key={l} className="dash-support text-fg-secondary">
              {l}
            </p>
          ))}
          {change.state === 'applied' && <p className="dash-support text-fg-muted">Check the next launch.</p>}
          <span className="sr-only">{copied ? 'Copied.' : ''}</span>
        </div>
        {active && (
          <Button type="button" variant="ghost" size="sm" className="max-sm:min-h-10" disabled={checking || cooling} onClick={checkNow}>
            {checking ? 'Checking' : 'Check now'}
          </Button>
        )}
      </div>
      {error && (
        <p role="alert" className="dash-support text-danger">
          <X className="mr-1 inline h-3 w-3" aria-hidden />
          {error}
        </p>
      )}
    </div>
  )
}
