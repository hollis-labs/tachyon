// Copied from Torque's lib/constants (only what the planner uses).
export const CONTAINER_STATUS_COLORS: Record<string, { bg: string; text: string; border: string; dot: string }> = {
  active:   { bg: 'bg-emerald-500/10', text: 'text-emerald-200', border: 'border-emerald-500/40', dot: 'bg-emerald-300' },
  inactive: { bg: 'bg-zinc-500/10',    text: 'text-zinc-400',    border: 'border-zinc-500/40',    dot: 'bg-zinc-500'    },
  completed:{ bg: 'bg-blue-500/10',    text: 'text-blue-200',    border: 'border-blue-500/40',    dot: 'bg-blue-300'    },
}
