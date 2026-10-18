import {
	cleanup,
	fireEvent,
	render,
	screen,
	waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createObjectsClient, type Project } from "./client";
import { ProjectsView, WorkstreamsView } from "./ObjectsView";

afterEach(cleanup);
const project: Project = {
	urn: "msg://agent/agent-mux/prj_test",
	kind: "project",
	display_name: "Tachyon",
	description: "Original",
	status: "active",
	updated_at: "2026-10-18T00:00:00Z",
};
const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), {
		status,
		headers: { "Content-Type": "application/json" },
	});

describe("Tether object client", () => {
	it("encodes registry identity and preserves explicit clearing without sending unrelated profile fields", async () => {
		const read = vi.fn();
		const write = vi.fn().mockResolvedValue(json(project));
		const client = createObjectsClient(read, write);
		await client.saveProject(
			{
				display_name: "Tachyon",
				description: "",
				tags: [],
				guidelines: "",
				entry_points: [],
			},
			project.urn,
		);
		expect(write.mock.calls[0][0]).toContain(
			`urn=${encodeURIComponent(project.urn)}`,
		);
		expect(JSON.parse(write.mock.calls[0][1].body)).toEqual({
			display_name: "Tachyon",
			description: "",
			tags: [],
			guidelines: "",
			entry_points: [],
		});
		expect(read).not.toHaveBeenCalled();
	});
	it("does not retry an uncertain create and distinguishes ensure from clear", async () => {
		const write = vi
			.fn()
			.mockRejectedValueOnce(new Error("disconnected"))
			.mockResolvedValueOnce(new Response(null, { status: 204 }))
			.mockResolvedValueOnce(json({ id: "ws1", status: "active" }));
		const client = createObjectsClient(vi.fn(), write);
		await expect(
			client.saveWorkstream({ name: "New", status: "active" }),
		).rejects.toThrow("Refresh and check");
		expect(write).toHaveBeenCalledTimes(1);
		expect(
			await client.sessionWorkstream("s1", { workstream_id: "" }),
		).toBeUndefined();
		expect(
			await client.sessionWorkstream("s1", { ensure: true }),
		).toMatchObject({ id: "ws1" });
		expect(JSON.parse(write.mock.calls[1][1].body)).toEqual({
			workstream_id: "",
		});
		expect(JSON.parse(write.mock.calls[2][1].body)).toEqual({ ensure: true });
	});
});

describe("Builder object management", () => {
	it("retains a project draft after refusal and only confirms success after a real save", async () => {
		const client = createObjectsClient();
		client.projects = vi.fn().mockResolvedValue([project]);
		client.saveProject = vi
			.fn()
			.mockRejectedValueOnce(new Error("Access denied"))
			.mockResolvedValueOnce({ ...project, description: "Edited" });
		render(<ProjectsView client={client} />);
		fireEvent.click(
			await screen.findByRole("button", { name: /Tachyon Original/ }),
		);
		fireEvent.change(screen.getByLabelText("Description"), {
			target: { value: "Edited" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save project" }));
		expect(await screen.findByRole("alert")).toHaveProperty(
			"textContent",
			"Access denied",
		);
		expect(
			(screen.getByLabelText("Description") as HTMLTextAreaElement).value,
		).toBe("Edited");
		fireEvent.click(screen.getByRole("button", { name: "Save project" }));
		expect(await screen.findByRole("status")).toHaveProperty(
			"textContent",
			"Tachyon saved · active",
		);
	});
	it("requires deprecation confirmation before mutating", async () => {
		const client = createObjectsClient();
		client.projects = vi.fn().mockResolvedValue([project]);
		client.deprecateProject = vi
			.fn()
			.mockResolvedValue({ ...project, status: "deprecated" });
		render(<ProjectsView client={client} />);
		fireEvent.click(
			await screen.findByRole("button", { name: /Tachyon Original/ }),
		);
		fireEvent.click(screen.getByRole("button", { name: "Deprecate project" }));
		expect(client.deprecateProject).not.toHaveBeenCalled();
		fireEvent.click(
			screen.getByRole("button", { name: "Confirm deprecation" }),
		);
		await waitFor(() =>
			expect(client.deprecateProject).toHaveBeenCalledWith(project.urn),
		);
	});
	it("edits workstream lifecycle and mints through the selected session lineage", async () => {
		const client = createObjectsClient();
		const workstream = {
			id: "ws1",
			name: "Build",
			status: "active" as const,
			created_at: "",
			updated_at: "",
		};
		client.workstreams = vi.fn().mockResolvedValue([workstream]);
		client.saveWorkstream = vi
			.fn()
			.mockResolvedValue({ ...workstream, status: "closed" });
		client.sessionWorkstream = vi.fn().mockResolvedValue(workstream);
		render(<WorkstreamsView client={client} />);
		fireEvent.click(
			await screen.findByRole("button", { name: /Build ws1 active/ }),
		);
		fireEvent.change(screen.getByLabelText("Status"), {
			target: { value: "closed" },
		});
		fireEvent.click(screen.getByRole("button", { name: "Save workstream" }));
		await waitFor(() =>
			expect(client.saveWorkstream).toHaveBeenCalledWith(
				{ name: "Build", workflow_id: "", status: "closed" },
				"ws1",
			),
		);
		fireEvent.click(
			await screen.findByRole("button", { name: "Session assignment" }),
		);
		fireEvent.change(screen.getByLabelText("Session ID"), {
			target: { value: "session-1" },
		});
		fireEvent.click(
			screen.getByRole("button", { name: "Find or create workstream" }),
		);
		await waitFor(() =>
			expect(client.sessionWorkstream).toHaveBeenCalledWith("session-1", {
				ensure: true,
				name: "",
			}),
		);
		expect(screen.getByText(/newest 100 matching workstreams/)).toBeTruthy();
	});
});
