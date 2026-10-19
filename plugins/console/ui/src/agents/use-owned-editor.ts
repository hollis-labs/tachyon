import { useRef, useState } from 'react'
import { builderDraft, builderIssues, builderPatch, type BuilderDraft } from './owned-builder-draft'
import { isRetired, newObjectID, type BuilderKind, type HistoryPage, type ObjectRecord, type OwnedBuilderClient } from './owned-builder-client'
import { useOwnedRecords } from './use-owned-records'

export type OwnedLifecycleAction = 'delete' | 'restore' | 'take_ownership'

/** Editing state for one kind of Tachyon-owned record. Views decide layout; behavior stays here. */
export function useOwnedEditor(kind: BuilderKind, client: OwnedBuilderClient) {
  const records = useOwnedRecords(client, kind)
  const [selected, setSelected] = useState<string>()
  const [base, setBase] = useState<ObjectRecord>()
  const [draft, setDraft] = useState<BuilderDraft>()
  const [review, setReview] = useState(false)
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(''), [message, setMessage] = useState('')
  const [history, setHistory] = useState<HistoryPage>()
  const pending = useRef(false)
  const dirty = !!draft && (!base || Object.keys(builderPatch(kind, draft, base)).length > 0)
  const issues = draft ? builderIssues(kind, draft) : []
  const locked = !!base && (base.ownership !== 'tachyon' || isRetired(base))
  const newer = base && records.rows.find(row => row.id === base.id && row.revision > base.revision)
  function install(row: ObjectRecord) {
    setSelected(row.id); setBase(row); setDraft(builderDraft(kind, row)); setReview(false); setHistory(undefined)
  }
  async function run(action: () => Promise<void>) {
    if (pending.current) return
    pending.current = true; setBusy(true); setProblem(''); setMessage('')
    try { await action() } catch (e) { setProblem(e instanceof Error ? e.message : 'The operation failed. Your draft is preserved.') }
    finally { pending.current = false; setBusy(false) }
  }
  const open = (id: string) => run(async () => {
    const row = await client.get(id)
    if (row.kind !== kind) throw new Error('Unexpected object kind.')
    install(row)
  })
  const create = () => { setSelected(newObjectID()); setBase(undefined); setDraft(builderDraft(kind)); setReview(false); setHistory(undefined); setProblem(''); setMessage('') }
  const save = () => run(async () => {
    if (!draft || !selected || issues.length || !review || locked) return
    const row = await client.save(kind, selected, builderPatch(kind, draft, base), base)
    records.commit(row); install(row); setMessage('Saved in Tachyon.')
  })
  const checkCreate = () => run(async () => {
    if (!selected || !draft || base) return
    const row = await client.get(selected)
    if (row.kind !== kind) throw new Error('Unexpected object kind.')
    records.commit(row); setBase(row); setReview(false)
    setMessage('The create was saved. Your current draft is preserved for comparison and further edits.')
  })
  const lifecycle = (action: OwnedLifecycleAction) => run(async () => {
    if (!base) return
    const row = await client.lifecycle(base, action)
    records.commit(row); install(row)
    setMessage(action === 'take_ownership' ? 'Tachyon now owns this definition. Provider provenance is retained.' : action === 'delete' ? 'Retired. History is retained.' : 'Restored.')
  })
  const readHistory = () => run(async () => {
    if (!base) return
    const page = await client.history(base.id, history?.next_after_revision ?? 0)
    setHistory(old => ({ ...page, items: [...(old?.items ?? []), ...page.items] }))
  })
  const set = (key: string, value: string | boolean) => { setDraft(d => d && ({ ...d, [key]: value })); setReview(false); setMessage('') }
  const discard = () => base ? void open(base.id) : (setDraft(undefined), setSelected(undefined), setReview(false))
  return { kind, client, records, selected, base, draft, review, setReview, busy, problem, message, history, dirty, issues, locked, newer, open, create, save, checkCreate, lifecycle, readHistory, set, discard }
}
export type OwnedEditor = ReturnType<typeof useOwnedEditor>
