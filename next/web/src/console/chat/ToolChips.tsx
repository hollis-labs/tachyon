import type { ToolChip } from './types'

const TONE = { running: 'border-warning/40 text-warning', ok: 'border-border text-fg-secondary', error: 'border-danger/50 text-danger' } as const
const MARK = { running: '...', ok: 'ok', error: 'err' } as const

export function ToolChips({ tools }: { tools: ToolChip[] }) {
  if (tools.length === 0) return null
  return (
    <ul aria-label="Tool calls" className="mt-1.5 flex flex-wrap gap-1.5">
      {tools.map((t) => (
        <li key={t.id} data-status={t.status} title={t.detail} className={`inline-flex items-center gap-1.5 border px-1.5 py-0.5 font-mono text-[10px] ${TONE[t.status]}`}>
          <span className={t.status === 'running' ? 'animate-pulse' : ''}>{MARK[t.status]}</span>
          <span>{t.name}</span>
          {t.detail && <span className="max-w-40 truncate text-fg-faint">{t.detail}</span>}
        </li>
      ))}
    </ul>
  )
}
