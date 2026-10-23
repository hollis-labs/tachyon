import { useState } from "react"
import { Button } from "@hollis-labs/design-components"
import { MessageBody } from "../chat/MessageBody"
import type { ApprovalItem, CardAnswer, CardReceipt } from "./client"

type Request = ApprovalItem["request_snapshot"]
const label = "font-mono dash-caption uppercase tracking-widest text-fg-faint"
const input = "mt-2 w-full border border-border bg-bg p-2 dash-control"

export interface CardField {
  id: string
  type: string
  label: string
  required: boolean
  options: { value: string; label: string }[]
}

// A card.form lists data.fields; a card.question is one field built from
// data.question (and optional data.options), answered under the id "answer".
export function cardFields(request: Request): CardField[] {
  const data = request.data ?? {}
  const options = (raw: unknown) =>
    (Array.isArray(raw) ? raw : []).map((o) =>
      typeof o === "string"
        ? { value: o, label: o }
        : { value: String((o as { value?: unknown }).value ?? ""), label: String((o as { label?: unknown }).label ?? (o as { value?: unknown }).value ?? "") },
    )
  if (request.kind === "card.question") {
    const opts = options(data.options)
    return [{ id: "answer", type: opts.length ? "enum" : "text", label: String(data.question ?? request.title), required: true, options: opts }]
  }
  if (request.kind !== "card.form" || !Array.isArray(data.fields)) return []
  return (data.fields as Record<string, unknown>[]).map((f) => ({
    id: String(f.id),
    type: String(f.type ?? "text"),
    label: String(f.label ?? f.id),
    required: f.required === true,
    options: options(f.options),
  }))
}

// Build the answers a form would submit; undefined when a required field is empty.
export function cardAnswers(fields: CardField[], values: Record<string, string | string[] | boolean>): CardAnswer[] | undefined {
  const answers: CardAnswer[] = []
  for (const f of fields) {
    const v = values[f.id]
    const empty = v === undefined || v === "" || (Array.isArray(v) && v.length === 0)
    if (empty) {
      if (f.required && f.type !== "bool") return undefined
      continue
    }
    if (f.type === "number") answers.push({ questionId: f.id, value: Number(v) })
    else if (f.type === "secret") answers.push({ questionId: f.id, value: { secret_ref: v } })
    else answers.push({ questionId: f.id, value: v })
  }
  return answers.length ? answers : undefined
}

export function CardData({ request }: { request: Request }) {
  const data = request.data ?? {}
  const shown = request.kind === "card.form" || request.kind === "card.question" ? undefined : data
  return (
    <>
      {request.kind === "card.question" && typeof data.question === "string" && <MessageBody body={data.question} />}
      {shown && Object.keys(shown).length > 0 && (
        <section>
          <h3 className={label}>Details</h3>
          <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-words font-mono dash-support text-fg-muted">{JSON.stringify(shown, null, 2)}</pre>
        </section>
      )}
      {!!request.effects?.length && (
        <section>
          <h3 className={label}>If approved this will</h3>
          <ul className="mt-2 list-disc pl-5 dash-control text-fg-muted">
            {request.effects.map((e, i) => (
              <li key={i}>{typeof e.summary === "string" ? e.summary : `${typeof e.op === "string" ? e.op + " " : ""}${e.target}`}</li>
            ))}
          </ul>
        </section>
      )}
    </>
  )
}

// Fields for a question or form card. onSubmit gets the typed answers.
export function CardForm({ request, busy, disabled, onSubmit }: {
  request: Request
  busy: boolean
  disabled: boolean
  onSubmit(answers: CardAnswer[]): void
}) {
  const fields = cardFields(request)
  const [values, setValues] = useState<Record<string, string | string[] | boolean>>({})
  const set = (id: string, v: string | string[] | boolean) => setValues((cur) => ({ ...cur, [id]: v }))
  const answers = cardAnswers(fields, values)
  return (
    <form
      className="space-y-3"
      onSubmit={(e) => {
        e.preventDefault()
        if (answers && !disabled) onSubmit(answers)
      }}
    >
      {fields.map((f) => (
        <label key={f.id} className="block dash-control text-fg-muted">
          {f.label}{f.required ? " *" : ""}
          {f.type === "enum" ? (
            <select aria-label={f.label} className={input} disabled={busy} value={String(values[f.id] ?? "")} onChange={(e) => set(f.id, e.target.value)}>
              <option value="">Choose…</option>
              {f.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          ) : f.type === "multi" ? (
            <select aria-label={f.label} multiple className={input} disabled={busy} value={(values[f.id] as string[]) ?? []} onChange={(e) => set(f.id, Array.from(e.target.selectedOptions, (o) => o.value))}>
              {f.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          ) : f.type === "bool" ? (
            <input aria-label={f.label} type="checkbox" className="ml-2" disabled={busy} checked={values[f.id] === true} onChange={(e) => set(f.id, e.target.checked)} />
          ) : f.type === "textarea" ? (
            <textarea aria-label={f.label} className={input} rows={3} disabled={busy} value={String(values[f.id] ?? "")} onChange={(e) => set(f.id, e.target.value)} />
          ) : (
            <input
              aria-label={f.label}
              className={input}
              disabled={busy}
              type={f.type === "number" ? "number" : f.type === "date" ? "date" : "text"}
              autoComplete="off"
              value={String(values[f.id] ?? "")}
              onChange={(e) => set(f.id, e.target.value)}
            />
          )}
          {f.type === "secret" && <span className="block dash-support text-fg-faint">Enter a secret reference (for example a vault key), never the secret itself.</span>}
        </label>
      ))}
      <Button type="submit" variant="outline" size="sm" className="font-mono dash-label" disabled={!answers || disabled || busy}>
        {busy ? "Submitting…" : "Submit"}
      </Button>
    </form>
  )
}

const outcome = { ok: "Applied", failed: "Failed", skipped: "Skipped" } as const

export function CardReceiptView({ receipt }: { receipt: CardReceipt }) {
  if (receipt.state === "none") return null
  return (
    <section className="rounded border border-border bg-surface/40 p-4" aria-label="Effects result">
      <h3 className={label}>Result · {receipt.state}</h3>
      {receipt.reason && <p className="mt-2 dash-control text-fg-muted">{receipt.reason}</p>}
      {receipt.effects && (
        <ul className="mt-2 space-y-1 dash-control text-fg-muted">
          {receipt.effects.map((e) => (
            <li key={e.index}>
              {outcome[e.status]} · {e.op ? `${e.op} ` : ""}{e.target}
              {e.detail ? ` — ${e.detail}` : ""}
              {e.error ? ` — ${e.error}` : ""}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
