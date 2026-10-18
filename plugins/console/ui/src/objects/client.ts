import { hostFetch } from "@tachyon/host-sdk";
import { operatorFetch } from "../operator/elevation";

export interface Project {
	urn: string;
	kind: "project";
	display_name: string;
	description?: string;
	tags?: string[];
	guidelines?: string;
	entry_points?: string[];
	status: string;
	external_ids?: { substrate: string; external_id: string }[];
	updated_at: string;
}

export interface Workstream {
	id: string;
	name?: string;
	workflow_id?: string;
	status: "active" | "closed";
	created_at: string;
	updated_at: string;
}

export type ProjectDraft = Pick<
	Project,
	"display_name" | "description" | "tags" | "guidelines" | "entry_points"
>;
export type WorkstreamDraft = Pick<
	Workstream,
	"name" | "workflow_id" | "status"
>;
const BASE = "/api/plugins/console/v1/tether";

export function createObjectsClient(
	read: typeof fetch = hostFetch,
	write: typeof fetch = operatorFetch,
) {
	async function request(
		path: string,
		method = "GET",
		body?: unknown,
		signal?: AbortSignal,
	) {
		let res: Response;
		try {
			res = await (method === "GET" ? read : write)(BASE + path, {
				method,
				signal,
				cache: "no-store",
				headers: {
					Accept: "application/json",
					"Content-Type": "application/json",
				},
				body: body === undefined ? undefined : JSON.stringify(body),
			});
		} catch (e) {
			if (signal?.aborted) throw e;
			throw new Error(
				method === "GET"
					? "Could not reach Tether. Try refreshing."
					: "The change could not be confirmed. Refresh and check the record before trying again.",
			);
		}
		if (res.status === 204) return undefined;
		const value = await res.json().catch(() => null);
		if (!res.ok) {
			const detail =
				value?.message ??
				value?.error?.message ??
				(typeof value?.error === "string" ? value.error : undefined);
			if (method !== "GET" && res.status >= 500)
				throw new Error(
					`${detail || "The change could not be confirmed."} Refresh and check the record before trying again.`,
				);
			throw new Error(detail || `Tether request failed (${res.status}).`);
		}
		if (!value || typeof value !== "object")
			throw new Error(
				"Tether returned an invalid response. Refresh before retrying a change.",
			);
		return value;
	}
	function project(value: Project): Project {
		if (
			value?.kind !== "project" ||
			typeof value.urn !== "string" ||
			typeof value.display_name !== "string"
		)
			throw new Error("Tether returned an invalid project.");
		return value;
	}
	function workstream(value: Workstream): Workstream {
		if (
			!value ||
			typeof value.id !== "string" ||
			!["active", "closed"].includes(value.status)
		)
			throw new Error("Tether returned an invalid workstream.");
		return value;
	}
	const projectPath = (urn: string) =>
		`/projects/item?urn=${encodeURIComponent(urn)}`;
	return {
		async projects(
			status = "active",
			signal?: AbortSignal,
		): Promise<Project[]> {
			const body = await request(
				`/projects?status=${encodeURIComponent(status)}`,
				"GET",
				undefined,
				signal,
			);
			if (!Array.isArray(body?.projects))
				throw new Error("Tether returned an invalid project list.");
			return body.projects.map(project);
		},
		async saveProject(draft: ProjectDraft, urn?: string): Promise<Project> {
			return project(
				await request(
					urn ? projectPath(urn) : "/projects",
					urn ? "PATCH" : "POST",
					draft,
				),
			);
		},
		async deprecateProject(urn: string): Promise<Project> {
			return project(await request(projectPath(urn), "DELETE"));
		},
		async restoreProject(urn: string): Promise<Project> {
			return project(
				await request(projectPath(urn), "PATCH", { status: "active" }),
			);
		},
		async workstreams(
			status = "",
			workflow = "",
			signal?: AbortSignal,
		): Promise<Workstream[]> {
			const query = new URLSearchParams({ status, workflow_id: workflow });
			const body = await request(
				`/workstreams?${query}`,
				"GET",
				undefined,
				signal,
			);
			if (!Array.isArray(body?.workstreams))
				throw new Error("Tether returned an invalid workstream list.");
			return body.workstreams.map(workstream);
		},
		async saveWorkstream(
			draft: WorkstreamDraft,
			id?: string,
		): Promise<Workstream> {
			const body = id
				? draft
				: { name: draft.name, workflow_id: draft.workflow_id };
			return workstream(
				await request(
					id ? `/workstreams/${encodeURIComponent(id)}` : "/workstreams",
					id ? "PATCH" : "POST",
					body,
				),
			);
		},
		async sessionWorkstream(
			session: string,
			body: {
				ensure?: boolean;
				workstream_id?: string;
				name?: string;
				workflow_id?: string;
			},
		): Promise<Workstream | undefined> {
			const result = await request(
				`/sessions/${encodeURIComponent(session)}/workstream`,
				"POST",
				body,
			);
			return result === undefined ? undefined : workstream(result);
		},
	};
}

export const objectsClient = createObjectsClient();
