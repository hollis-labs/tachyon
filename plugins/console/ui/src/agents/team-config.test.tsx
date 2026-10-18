import { afterEach, expect, it, vi } from "vitest";
import {
	cleanup,
	fireEvent,
	render,
	screen,
	within,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { dataRefreshRegistry } from "@tachyon/host-sdk";
import { AgentDirectoryView } from "./AgentDirectory";
import { AgentsClientProvider } from "./context";
import { createCatalogAgentsClient, DEMO_SAVE_MESSAGE } from "./catalog-client";
import {
	newTeamDraft,
	parseTeamPolicy,
	teamDraftIssues,
	teamPreview,
} from "./team-config";

afterEach(cleanup);
const policy = `version: 1
session_formation: true
launches:
  - launch: team-worker
    runtime: claude
    definition: {id: worker, revision: r1, digest: 'sha256:${"a".repeat(64)}'}
    source: catalog:team-definitions/worker.md
`;
function setup(importedTeams: unknown[] = []) {
	const saved = new Map<string, import('./owned-teams').OwnedTeam>();
	const fetcher = vi.fn(
		async (input: RequestInfo | URL, init?: RequestInit) => {
			const url = new URL(String(input), 'http://localhost');
			if (url.pathname.includes('/data/providers/tachyon/verbs/')) {
				const verb = url.pathname.split('/').pop()!;
				const q = JSON.parse(init?.body as string || url.searchParams.get('input') || '{}');
				let data: unknown;
				if (verb === 'objects.list') data = { items: [...saved.values()] };
				else if (verb === 'objects.get') data = saved.get(q.id);
				else if (verb === 'objects.create') {
					data = { id: q.id, kind: 'team', ownership: 'tachyon', name: q.definition.name, revision: 1, definition: q.definition };
					saved.set(q.id, data as import('./owned-teams').OwnedTeam);
				} else throw new Error('Unexpected owned verb');
				return new Response(JSON.stringify({ version: 1, provider: 'tachyon', verb, data }));
			}
			if (init?.method && init.method !== "GET")
				throw new Error("Unexpected write");
			const kind = String(input).split("/").pop();
			return new Response(
				JSON.stringify(
				kind === 'imported-teams' ? { source: 'agent-os Tether', imported_at: '2026-10-17T18:00:00Z', teams: importedTeams } : kind === "team-policy"
						? { content: policy }
						: kind === "agents"
							? { agents: [{ id: "worker", name: "Worker" }] }
							: { launches: [] },
				),
			);
		},
	);
	const client = createCatalogAgentsClient(fetcher);
	render(
		<MemoryRouter>
			<AgentsClientProvider client={client}>
				<AgentDirectoryView />
			</AgentsClientProvider>
		</MemoryRouter>,
	);
	return { client, fetcher };
}
it("preserves exact definition pins and refuses missing bindings or unsupported concurrent seats", () => {
	const p = parseTeamPolicy(policy);
	const d = { ...newTeamDraft(), id: "review-team", name: "Review team" };
	expect(teamDraftIssues(d, p)).toContain(
		"workers needs a launch from the activation policy.",
	);
	d.slots.forEach((s) => {
		s.launch = "team-worker";
	});
	expect(teamDraftIssues(d, p)).toEqual([]);
	expect(teamPreview(d, p).slots[0].definition_ref).toEqual(
		p.bindings[0].definition,
	);
	d.slots[0].max = 2;
	expect(teamDraftIssues(d, p).some((s) => s.includes("worker pools"))).toBe(
		true,
	);
	expect(() =>
		parseTeamPolicy(policy.replace("version: 1", "version: 9")),
	).toThrow("Unsupported");
	expect(() => parseTeamPolicy(policy.replace("sha256:", "broken:"))).toThrow(
		"invalid launch",
	);
});
it("configures teams from live bindings, keeps drafts across tabs and refresh, and saves owned configuration", async () => {
	const { fetcher } = setup();
	fireEvent.click(
		await screen.findByRole("tab", { name: "Teams" }),
	);
	await screen.findByText("team-worker", { selector: "span" });
	fireEvent.click(screen.getByRole("button", { name: "New team draft" }));
	fireEvent.change(screen.getByLabelText("Team name"), {
		target: { value: "Review team" },
	});
	for (const select of screen.getAllByLabelText("Launch binding"))
		fireEvent.change(select, { target: { value: "team-worker" } });
	fireEvent.click(
		screen.getByRole("tab", { name: "Assignments" }),
	);
	await screen.findByText("No live assignment feed connected");
	fireEvent.click(screen.getByRole("tab", { name: "Teams" }));
	dataRefreshRegistry("catalog").refresh();
	expect((screen.getByLabelText("Team name") as HTMLInputElement).value).toBe(
		"Review team",
	);
	fireEvent.click(screen.getByRole("button", { name: "Review team draft" }));
	const review = await screen.findByRole("article", {
		name: "Team draft review",
	});
	await screen.findByText(
		"Strict authority · coordinator coordinator · history none",
	);
	fetcher.mockClear();
	fireEvent.click(
		within(review).getByRole("button", { name: "Save team definition" }),
	);
	await within(review).findByText('Team definition saved in Tachyon.');
	const write = fetcher.mock.calls.find(([, init]) => init?.method === 'POST')!;
	expect(String(write[0])).toContain('/data/providers/tachyon/verbs/objects.create');
	const body = JSON.parse(write[1]!.body as string);
	expect(body.definition.properties.builder_team.preview.slots[0].definition_ref.revision).toBe('r1');
	expect(body.definition.coordinator).toBe('coordinator');
	fireEvent.click(within(review).getByRole('button', { name: 'Discard draft and reload saved version' }));
	await screen.findByRole('article', { name: 'Configure team draft' });
	expect((screen.getByLabelText('Team name') as HTMLInputElement).value).toBe('Review team');
	fireEvent.change(screen.getByLabelText('Team name'), { target: { value: 'Keep my conflicting edit' } });
	fireEvent.click(screen.getByRole('button', { name: 'Review team draft' }));
	const original = fetcher.getMockImplementation()!;
	fetcher.mockImplementation((input, init) => String(input).endsWith('/objects.patch')
		? Promise.resolve(new Response(JSON.stringify({ error: 'conflict' }), { status: 409 })) : original(input, init));
	fireEvent.click(screen.getByRole('button', { name: 'Save team definition' }));
	await screen.findByText(/The saved team changed/);
	dataRefreshRegistry('owned-teams').refresh();
	fireEvent.click(screen.getByRole('button', { name: 'Continue editing' }));
	expect((screen.getByLabelText('Team name') as HTMLInputElement).value).toBe('Keep my conflicting edit');
	expect(screen.getByText(/The saved team changed/)).toBeTruthy();
	expect(screen.queryByText("Apply")).toBeNull();
});
it("stages and reviews a work assignment without writing or inventing live assignments", async () => {
	const { fetcher, client } = setup();
	fireEvent.click(
		await screen.findByRole("tab", { name: "Assignments" }),
	);
	await screen.findByText("No live assignment feed connected");
	fireEvent.change(screen.getByLabelText("Agent"), {
		target: { value: "worker" },
	});
	fireEvent.change(screen.getByLabelText("Target id"), {
		target: { value: "CW-demo" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Stage" }));
	fireEvent.click(screen.getByRole("button", { name: "Review plan (1)" }));
	const save = await screen.findByRole("button", {
		name: "Save demonstration",
	});
	fetcher.mockClear();
	fireEvent.click(save);
	await screen.findByText(DEMO_SAVE_MESSAGE);
	expect(fetcher).not.toHaveBeenCalled();
	expect(await client.listAssignments()).toEqual([]);
});
it("surfaces team policy read failures and supports retry without replacing a draft", async () => {
	const { client } = setup();
	await screen.findByRole("list", { name: "Agents" });
	client.getTeamActivationPolicy = vi.fn(client.getTeamActivationPolicy!).mockRejectedValueOnce(new Error('Tether team policy read failed (401).'));
	fireEvent.click(screen.getByRole("tab", { name: "Teams" }));
	await screen.findByRole("alert");
	fireEvent.click(screen.getByRole("button", { name: "New team draft" }));
	fireEvent.change(screen.getByLabelText("Team name"), {
		target: { value: "Keep me" },
	});
	fireEvent.click(screen.getByRole("button", { name: "Retry team policy" }));
	await screen.findByText("team-worker", { selector: "span" });
	expect((screen.getByLabelText("Team name") as HTMLInputElement).value).toBe(
		"Keep me",
	);
});

it('shows imported saved versions separately from drafts without inventing membership or dropping source fields', async () => {
  const base = { id: 'first-team', name: 'First team', slots: [{ name: 'workers', role: '', min: 1, max: 1, definition_ref: { id: 'claude-agent', revision: 'r1', digest: 'sha256:original' } }], authority: { mode: 'strict', grants: [{ verb: 'may_delegate' }] } }
  setup([{ ...base, version: 1 }, { ...base, version: 3 }])
  fireEvent.click(await screen.findByRole('tab', { name: 'Teams' }))
  const detail = await screen.findByRole('article', { name: 'first-team version 3 snapshot' })
  expect(within(detail).getByText('Imported · read only')).toBeTruthy()
  expect(within(detail).getByText('No role specified')).toBeTruthy()
  expect(within(detail).getByText('claude-agent@r1')).toBeTruthy()
  expect(within(detail).queryByRole('button', { name: 'Save demonstration' })).toBeNull()
  fireEvent.click(within(screen.getByRole('list', { name: 'Saved teams' })).getByRole('button', { name: /version 1/ }))
  await screen.findByRole('article', { name: 'first-team version 1 snapshot' })
  fireEvent.click(screen.getByRole('button', { name: 'New team draft' }))
  fireEvent.change(screen.getByLabelText('Team name'), { target: { value: 'My draft' } })
  fireEvent.click(within(screen.getByRole('list', { name: 'Saved teams' })).getByRole('button', { name: /version 3/ }))
  fireEvent.click(within(screen.getByRole('list', { name: 'Team drafts' })).getByRole('button', { name: /My draft/ }))
  expect((screen.getByLabelText('Team name') as HTMLInputElement).value).toBe('My draft')
})
