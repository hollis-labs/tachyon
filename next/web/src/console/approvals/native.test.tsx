import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react"
import { afterEach, expect, it, vi } from "vitest"
import { type ApprovalClient, type ApprovalItem, createApprovalClient } from "./client"
import { NativeGateDetail } from "./native-detail"

afterEach(cleanup)
const item: ApprovalItem = {
  item_id: "native-item", state: "presented", revision: 7, presented_projection_revision: 7,
  enqueued_at: "2026-11-15T00:00:00Z", updated_at: "2026-11-15T00:00:00Z",
  request_snapshot: { kind: "gate_native_v1", title: "", source: {application_id:"gatecrasher",agent_id:"operative"} },
  native_gate: {title:"tool: operation-1",scope:"once",adapter:{adapter_id:"codex",protocol:"jsonrpc",launch_mode:"app-server"},enforcement_point:"host",operation_digest:{scheme:"sha256",descriptor_version:"tool-v1",value:"a".repeat(64)},options:[
    {id:"opaque-Allow/1",kind:"allow",expires_at:"2999-01-01T00:00:00Z"},
    {id:"opaque-Cancel/3",kind:"cancel",expires_at:"2999-01-01T00:00:00Z"},
  ]},
}
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status,headers:{"Content-Type":"application/json"}})

it("requires an explicit exact choice and bypasses the old decision/hold path", async () => {
  const decideNative = vi.fn().mockResolvedValue({state:"resolved"})
  const decide = vi.fn()
  const client = {decideNative,decide} as unknown as ApprovalClient
  const refresh = vi.fn().mockResolvedValue(undefined)
  render(<NativeGateDetail item={item} client={client} ready error="" refresh={refresh} onChanged={vi.fn()} />)
  const submit = screen.getByRole("button",{name:"Record exact choice"})
  expect((submit as HTMLButtonElement).disabled).toBe(true)
  expect(screen.getAllByRole("radio").every((r) => !(r as HTMLInputElement).checked)).toBe(true)
  fireEvent.click(screen.getByRole("radio",{name:"cancel · opaque-Cancel/3 · Once"}))
  fireEvent.click(submit)
  await waitFor(() => expect(decideNative).toHaveBeenCalledWith(item,"opaque-Cancel/3"))
  expect(decide).not.toHaveBeenCalled()
  expect(screen.queryByText(/undo/i)).toBeNull()
})

it("refuses after a conflict and clears the choice until refreshed review", async () => {
  const decideNative = vi.fn().mockRejectedValue(new Error("Request changed"))
  render(<NativeGateDetail item={item} client={{decideNative} as unknown as ApprovalClient} ready error="" refresh={vi.fn().mockResolvedValue(undefined)} onChanged={vi.fn()} />)
  fireEvent.click(screen.getByRole("radio",{name:"allow · opaque-Allow/1 · Once"}))
  fireEvent.click(screen.getByRole("button",{name:"Record exact choice"}))
  await screen.findByRole("alert")
  expect(screen.getAllByRole("radio").every((r) => !(r as HTMLInputElement).checked)).toBe(true)
  expect((screen.getByRole("button",{name:"Record exact choice"}) as HTMLButtonElement).disabled).toBe(true)
  expect(decideNative).toHaveBeenCalledTimes(1)
})

it("sends only the exact native kind/ID with reviewed revisions, and never retries a stale projection", async () => {
  const fetcher=vi.fn().mockResolvedValueOnce(json(item)).mockResolvedValueOnce(json({state:"resolved"}))
  await createApprovalClient(fetcher).decideNative(item,"opaque-Allow/1")
  expect(JSON.parse(fetcher.mock.calls[1][1].body)).toEqual({expected_revision:7,presented_projection_revision:7,response:{kind:"gate_native_v1",decision:"opaque-Allow/1"}})
  const stale=vi.fn().mockResolvedValueOnce(json({...item,revision:8,presented_projection_revision:8}))
  await expect(createApprovalClient(stale).decideNative(item,"opaque-Allow/1")).rejects.toMatchObject({status:409})
  expect(stale).toHaveBeenCalledTimes(1)
})

it("shows native Cancel separately from withdrawal and never calls it execution success", () => {
  const terminal={...item,state:"resolved",terminal_outcome:{item_id:item.item_id,state:"resolved",resolution:{response:{kind:"gate_native_v1" as const,decision:"opaque-Cancel/3"},resolved_at:"2026-11-15T00:00:00Z",participant:{principal_ref:"user:one",authority:"tachyon/identity",assurance:"operator-token"}}}}
  render(<NativeGateDetail item={terminal} client={{} as ApprovalClient} ready error="" refresh={vi.fn()} onChanged={vi.fn()} />)
  expect(screen.getByRole("status").textContent).toContain("Recorded cancel · opaque-Cancel/3 · Once")
  expect(screen.queryByRole("radio")).toBeNull()
})
