import { useState } from 'react'
import { Chip } from '@planner/shared'
import { Act, Block, LINK } from './Block'
import { SquareTerminal } from 'lucide-react'
import type { RunView } from './types'

const clock = (s: number) => `${String(Math.floor(s / 60)).padStart(2, '0')}:${String(s % 60).padStart(2, '0')}`

/**
 * The terminal run card (addendum 01M6527WVSBPEWX3MN2WEBRPNE): one line when finished, live output while running.
 * Credentials never appear here; output arrives already redacted from the Run record.
 */
export function RunCard({ run, onStop, onOpenDrawer, onClose, onCloseOnComplete }: { run: RunView; onStop?: () => void; onOpenDrawer?: () => void; onClose?: () => void; onCloseOnComplete?: (v: boolean) => void }) {
  const [showOutput, setShowOutput] = useState(false)
  const [closeWhenDone, setCloseWhenDone] = useState(run.closeOnComplete)
  if (run.closed) return null
  const running = run.state === 'running'
  const bad = run.state === 'failed' || run.state === 'stopped'
  const status = running ? `● running · ${clock(run.seconds)}` : run.state === 'stopped' ? '■ stopped' : bad ? `✕ exit ${run.exitCode ?? '?'} · ${run.seconds}s` : `✓ exit ${run.exitCode ?? 0} · ${run.seconds}s`
  return (
    <Block label={`Terminal run: ${run.command}`} icon={<SquareTerminal className="size-3.5" />} caption="Terminal" title={`${run.machine} · run by ${run.requestedBy} · ${run.approval === 'policy' ? 'allowed by policy' : 'approved'}`} aside={<Chip tone={bad ? 'danger' : 'neutral'}>{status}</Chip>}>
      {running ? (
        <>
          <pre role="log" aria-label="Terminal output" className="max-h-48 overflow-auto whitespace-pre-wrap rounded-control border border-border-subtle bg-bg-elevated px-2.5 py-2 font-mono text-xs leading-relaxed text-fg-secondary"><span className="text-fg-muted">{run.machine} $ </span>{run.command}{'\n'}{run.output}</pre>
          <div className="flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1.5 text-xs text-fg-secondary">
              <button type="button" role="checkbox" aria-checked={closeWhenDone} aria-label="Close when done" onClick={() => { setCloseWhenDone(!closeWhenDone); onCloseOnComplete?.(!closeWhenDone) }} className={`grid size-3 place-items-center rounded-sm border text-[9px] leading-none focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring ${closeWhenDone ? 'border-fg bg-fg/90 text-bg' : 'border-fg-muted'}`}>{closeWhenDone ? '✓' : ''}</button>
              Close when done
            </span>
            <span className="flex-1" />
            <Act kbd="⌃C" onClick={onStop}>Stop</Act>
            <Act kbd="⌃T" onClick={onOpenDrawer}>Open in drawer</Act>
          </div>
        </>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2.5 text-xs">
            <code className="rounded-sm bg-surface px-1.5 py-0.5 font-mono text-xs">{run.command}</code>
            <span className="flex-1" />
            <button type="button" aria-expanded={showOutput} onClick={() => setShowOutput((o) => !o)} className={LINK}>Output {showOutput ? '▾' : '▸'}</button>
            <Act className="h-[22px]" onClick={onClose}>Close</Act>
          </div>
          {showOutput && (
            <pre role="log" aria-label="Terminal output" className="max-h-48 overflow-auto whitespace-pre-wrap rounded-control border border-border-subtle bg-bg-elevated px-2.5 py-2 font-mono text-xs leading-relaxed text-fg-secondary">{run.output || 'No output.'}{run.truncated ? '\n… output truncated' : ''}</pre>
          )}
        </>
      )}
    </Block>
  )
}
