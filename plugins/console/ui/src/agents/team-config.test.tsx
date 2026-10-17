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
function setup() {
	const fetcher = vi.fn(
		async (input: RequestInfo | URL, init?: RequestInit) => {
			if (init?.method && init.method !== "GET")
				throw new Error("Unexpected write");
			const kind = String(input).split("/").pop();
			return new Response(
				JSON.stringify(
					kind === "team-policy"
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
it("configures teams from live bindings, keeps drafts across tabs and refresh, and performs a no-op save", async () => {
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
		within(review).getByRole("button", { name: "Save demonstration" }),
	);
	await within(review).findByText(DEMO_SAVE_MESSAGE);
	expect(fetcher).not.toHaveBeenCalled();
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
	const { fetcher } = setup();
	await screen.findByRole("list", { name: "Agents" });
	fetcher.mockResolvedValueOnce(new Response("", { status: 401 }));
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
