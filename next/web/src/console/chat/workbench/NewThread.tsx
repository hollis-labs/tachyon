import { CAPTION, Chip } from '@planner/shared'

const SUGGESTIONS = ['Give me a status summary.', 'Draft a plan for what is next.', 'List what you are waiting on from me.']

/** A thread with no messages: who the agent is and three things to start with. Picking one fills the composer. */
export function NewThread({ agent, role, onPick }: { agent: string; role?: string; onPick: (text: string) => void }) {
  return (
    <div className="mx-auto flex w-full max-w-[760px] flex-col items-start gap-3 py-6">
      <span className={CAPTION}>New chat</span>
      <h2 className="m-0 flex items-center gap-2 text-control font-semibold normal-case tracking-normal text-fg">{agent}{role && <Chip square>{role}</Chip>}</h2>
      <p className="m-0 text-xs text-fg-muted">Nothing has been said yet. Ask {agent} something, or start with one of these.</p>
      <div className="flex flex-wrap gap-2">
        {SUGGESTIONS.map((s) => <button key={s} type="button" onClick={() => onPick(s)} className="rounded-control border border-border px-2.5 py-1 text-label text-fg-secondary hover:bg-surface-hover focus-visible:outline focus-visible:outline-1 focus-visible:outline-ring">{s}</button>)}
      </div>
    </div>
  )
}
