import { Kbd } from '@planner/shared'
import { useScratchpad } from './model'

/** Local notes. Not sent to the agent unless referenced; ⌘↵ puts the notes into the composer for review. */
export function ScratchPane({ threadRef, onInsert }: { threadRef: string | undefined; onInsert: (text: string) => void }) {
  const pad = useScratchpad(threadRef)
  return (
    <>
      <div className="flex items-center gap-2 text-label text-fg-muted">
        <span>Not sent to the agent unless you reference it</span>
        <span className="flex-1" />
        <span aria-live="polite">{pad.state === 'saving' ? 'Saving' : pad.state === 'saved' ? 'Saved' : ''}</span>
      </div>
      <textarea
        aria-label="Scratchpad"
        value={pad.body}
        onChange={(e) => pad.change(e.target.value)}
        onKeyDown={(e) => { if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && pad.body.trim()) { e.preventDefault(); onInsert(pad.body) } }}
        placeholder="- Notes for yourself. @agent to reference the agent."
        className="min-h-0 flex-1 resize-none rounded-none border-0 bg-transparent p-0 font-mono text-xs leading-relaxed text-fg-secondary placeholder:text-fg-faint focus-visible:bg-transparent!"
      />
      {pad.error && <p role="alert" className="text-xs text-danger">{pad.error} <button type="button" onClick={pad.retry} className="underline">Retry</button></p>}
      <p className="text-label text-fg-muted">Put these notes in the message <Kbd>⌘↵</Kbd></p>
    </>
  )
}
