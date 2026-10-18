import { AdminInput, AdminSelect, AdminTextarea } from '@hollis-labs/kit-admin'
import { EmptyState, Input } from "@hollis-labs/design-components";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { Field, inputCls } from "../agents/AgentEditor";
import { Btn, Chip } from "../agents/ui";
import {
	objectsClient,
	type Project,
	type ProjectDraft,
	type Workstream,
} from "./client";

type Client = typeof objectsClient;
const area = `${inputCls} min-h-24 py-2`;
const message = (e: unknown) =>
	e instanceof Error ? e.message : "The request failed.";
const lines = (s: string) => [
	...new Set(
		s
			.split("\n")
			.map((v) => v.trim())
			.filter(Boolean),
	),
];

function Notice({ error, note }: { error?: string; note?: string }) {
	return (
		<>
			{error && (
				<p
					role="alert"
					className="border-b border-danger/30 px-4 py-3 text-sm text-danger"
				>
					{error}
				</p>
			)}
			{note && (
				<p
					role="status"
					className="border-b border-success/30 px-4 py-3 text-sm text-success"
				>
					{note}
				</p>
			)}
		</>
	);
}

function useRows<T>(load: (signal: AbortSignal) => Promise<T[]>) {
	const [rows, setRows] = useState<T[]>([]);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState("");
	const [version, refresh] = useState(0);
	useEffect(() => {
		const controller = new AbortController();
		setLoading(true);
		setError("");
		void load(controller.signal).then(
			(v) => {
				if (!controller.signal.aborted) {
					setRows(v);
					setLoading(false);
				}
			},
			(e) => {
				if (!controller.signal.aborted) {
					setError(message(e));
					setLoading(false);
				}
			},
		);
		return () => controller.abort();
	}, [load, version]);
	return { rows, loading, error, reload: () => refresh((v) => v + 1) };
}

function Collection({
	loading,
	error,
	empty,
	children,
}: {
	loading: boolean;
	error: string;
	empty: boolean;
	children: ReactNode;
}) {
	if (loading)
		return (
			<p role="status" className="p-4 text-sm text-fg-muted">
				Loading…
			</p>
		);
	if (error)
		return (
			<div className="p-4">
				<EmptyState
					variant="error"
					title="Could not load records"
					description={error}
				/>
			</div>
		);
	if (empty)
		return (
			<div className="p-4">
				<EmptyState
					variant="empty"
					title="No matching records"
					description="Change the filter or create a new record."
				/>
			</div>
		);
	return <>{children}</>;
}

export function ProjectsView({ client = objectsClient }: { client?: Client }) {
	const [status, setStatus] = useState("active");
	const [search, setSearch] = useState("");
	const [editing, setEditing] = useState<Project | null | undefined>();
	const [note, setNote] = useState("");
	const collection = useRows(
		useCallback(
			(s: AbortSignal) => client.projects(status, s),
			[client, status],
		),
	);
	const rows = collection.rows.filter((p) =>
		`${p.display_name} ${p.description ?? ""} ${p.urn} ${(p.tags ?? []).join(" ")}`
			.toLowerCase()
			.includes(search.toLowerCase()),
	);
	return (
		<div>
			<p className="border-b border-border/80 px-4 py-3 text-sm text-fg-muted">
				Project identities and shared guidance. Saves update Tether immediately.
			</p>
			<Notice note={note} />
			{editing !== undefined ? (
				<ProjectEditor
					key={editing?.urn ?? "new"}
					project={editing}
					client={client}
					onCancel={() => setEditing(undefined)}
					onSaved={(p) => {
						setEditing(undefined);
						setNote(`${p.display_name} saved · ${p.status}`);
						collection.reload();
					}}
				/>
			) : (
				<>
					<div className="flex flex-wrap items-center gap-3 border-b border-border/80 px-4 py-3">
						<Input
							aria-label="Search projects"
							placeholder="Search projects"
							value={search}
							onChange={(e) => setSearch(e.target.value)}
							className="max-w-xs"
						/>
						<AdminSelect
							aria-label="Project status"
							className={`${inputCls} max-w-40`}
							value={status}
							onChange={(e) => setStatus(e.target.value)}
						>
							{["active", "deprecated", "archived", "merged"].map((v) => (
								<option key={v}>{v}</option>
							))}
						</AdminSelect>
						<Btn onClick={collection.reload} disabled={collection.loading}>
							Refresh
						</Btn>
						<Btn
							tone="primary"
							className="ml-auto"
							onClick={() => {
								setNote("");
								setEditing(null);
							}}
						>
							New project
						</Btn>
					</div>
					<Collection
						loading={collection.loading}
						error={collection.error}
						empty={!rows.length}
					>
						<ul aria-label="Projects">
							{rows.map((p) => (
								<li key={p.urn}>
									<button
										type="button"
										className="flex w-full items-center gap-4 border-b border-border/80 px-4 py-4 text-left hover:bg-panel/60  "
										onClick={() => {
											setNote("");
											setEditing(p);
										}}
									>
										<span className="min-w-0 flex-1">
											<span className="block font-mono text-sm text-fg">
												{p.display_name}
											</span>
											<span className="mt-1 block text-sm text-fg-muted">
												{p.description || "No description"}
											</span>
											<span className="mt-1 block break-all font-mono text-xs text-fg-faint">
												{p.urn}
											</span>
										</span>
										<Chip>{p.status}</Chip>
									</button>
								</li>
							))}
						</ul>
					</Collection>
				</>
			)}
		</div>
	);
}

function ProjectEditor({
	project,
	client,
	onCancel,
	onSaved,
}: {
	project: Project | null;
	client: Client;
	onCancel(): void;
	onSaved(p: Project): void;
}) {
	const [name, setName] = useState(project?.display_name ?? "");
	const [description, setDescription] = useState(project?.description ?? "");
	const [tags, setTags] = useState((project?.tags ?? []).join("\n"));
	const [guidelines, setGuidelines] = useState(project?.guidelines ?? "");
	const [entries, setEntries] = useState(
		(project?.entry_points ?? []).join("\n"),
	);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const [confirm, setConfirm] = useState(false);
	async function run(action: () => Promise<Project>) {
		setBusy(true);
		setError("");
		try {
			onSaved(await action());
		} catch (e) {
			setError(message(e));
		} finally {
			setBusy(false);
		}
	}
	const draft: ProjectDraft = {
		display_name: name.trim(),
		description,
		tags: lines(tags),
		guidelines,
		entry_points: lines(entries),
	};
	return (
		<form
			aria-label="Project editor"
			onSubmit={(e) => {
				e.preventDefault();
				void run(() => client.saveProject(draft, project?.urn));
			}}
		>
			<Notice error={error} />
			<fieldset disabled={busy} className="space-y-4 p-5">
				<div className="flex flex-wrap items-center gap-3">
					<h2 className="font-mono text-sm uppercase">
						{project ? "Edit project" : "New project"}
					</h2>
					{project && <Chip>{project.status}</Chip>}
					<Btn className="ml-auto" onClick={onCancel}>
						Back to projects
					</Btn>
				</div>
				{project && (
					<p className="break-all font-mono text-xs text-fg-faint">
						{project.urn}
					</p>
				)}
				<Field label="Project name" htmlFor="project-name">
					<AdminInput
						id="project-name"
						required
						maxLength={200}
						className={inputCls}
						value={name}
						onChange={(e) => setName(e.target.value)}
					/>
				</Field>
				<Field label="Description" htmlFor="project-description">
					<AdminTextarea
						id="project-description"
						className={area}
						value={description}
						onChange={(e) => setDescription(e.target.value)}
					/>
				</Field>
				<div className="grid gap-4 md:grid-cols-2">
					<Field label="Tags · one per line" htmlFor="project-tags">
						<AdminTextarea
							id="project-tags"
							className={area}
							value={tags}
							onChange={(e) => setTags(e.target.value)}
						/>
					</Field>
					<Field label="Entry points · one per line" htmlFor="project-entries">
						<AdminTextarea
							id="project-entries"
							className={area}
							value={entries}
							onChange={(e) => setEntries(e.target.value)}
						/>
					</Field>
				</div>
				<Field label="Guidelines" htmlFor="project-guidelines">
					<AdminTextarea
						id="project-guidelines"
						className={`${area} min-h-40`}
						value={guidelines}
						onChange={(e) => setGuidelines(e.target.value)}
					/>
				</Field>
				{!!project?.external_ids?.length && (
					<div>
						<h3 className="mb-2 text-sm text-fg-muted">Linked identifiers</h3>
						<dl>
							{project.external_ids.map((v) => (
								<div key={v.substrate} className="flex gap-3 font-mono text-xs">
									<dt>{v.substrate}</dt>
									<dd>{v.external_id}</dd>
								</div>
							))}
						</dl>
					</div>
				)}
				<div className="flex flex-wrap gap-2">
					<Btn type="submit" tone="primary" disabled={!name.trim() || busy}>
						{busy ? "Saving…" : project ? "Save project" : "Create project"}
					</Btn>
					{project?.status === "active" && (
						<Btn tone="danger" onClick={() => setConfirm(true)}>
							Deprecate project
						</Btn>
					)}
					{project?.status === "deprecated" && (
						<Btn
							onClick={() => void run(() => client.restoreProject(project.urn))}
						>
							Restore project
						</Btn>
					)}
				</div>
				{confirm && project && (
					<div className="space-y-3 border border-danger/40 p-3">
						<p className="text-sm">
							Deprecate {project.display_name}? Its identity and history remain
							available. Repository files and launch settings are unchanged.
						</p>
						<div className="flex gap-2">
							<Btn
								tone="danger"
								onClick={() =>
									void run(() => client.deprecateProject(project.urn))
								}
							>
								Confirm deprecation
							</Btn>
							<Btn onClick={() => setConfirm(false)}>Keep project</Btn>
						</div>
					</div>
				)}
			</fieldset>
		</form>
	);
}

export function WorkstreamsView({
	client = objectsClient,
}: {
	client?: Client;
}) {
	const [status, setStatus] = useState("");
	const [search, setSearch] = useState("");
	const [workflow, setWorkflow] = useState("");
	const [editing, setEditing] = useState<Workstream | null | undefined>();
	const [sessionForm, setSessionForm] = useState(false);
	const [note, setNote] = useState("");
	const collection = useRows(
		useCallback(
			(s: AbortSignal) => client.workstreams(status, workflow, s),
			[client, status, workflow],
		),
	);
	const rows = collection.rows.filter((w) =>
		`${w.name ?? ""} ${w.id} ${w.workflow_id ?? ""}`
			.toLowerCase()
			.includes(search.toLowerCase()),
	);
	return (
		<div>
			<p className="border-b border-border/80 px-4 py-3 text-sm text-fg-muted">
				Durable work across sessions. Close finished workstreams or reopen them
				to continue.
			</p>
			<Notice note={note} />
			{editing !== undefined ? (
				<WorkstreamEditor
					key={editing?.id ?? "new"}
					workstream={editing}
					client={client}
					onCancel={() => setEditing(undefined)}
					onSaved={(w) => {
						setEditing(undefined);
						setNote(`Saved ${w.name || w.id} · ${w.status}`);
						collection.reload();
					}}
				/>
			) : sessionForm ? (
				<SessionWorkstreamForm
					client={client}
					onCancel={() => setSessionForm(false)}
					onSaved={(text) => {
						setSessionForm(false);
						setNote(text);
						collection.reload();
					}}
				/>
			) : (
				<>
					<div className="flex flex-wrap items-center gap-3 border-b border-border/80 px-4 py-3">
						<Input
							aria-label="Search workstreams"
							placeholder="Search loaded workstreams"
							value={search}
							onChange={(e) => setSearch(e.target.value)}
							className="max-w-xs"
						/>
						<AdminSelect
							aria-label="Workstream status filter"
							className={`${inputCls} max-w-40`}
							value={status}
							onChange={(e) => setStatus(e.target.value)}
						>
							<option value="">All statuses</option>
							<option value="active">Active</option>
							<option value="closed">Closed</option>
						</AdminSelect>
						<Input
							aria-label="Filter by workflow ID"
							placeholder="Exact workflow ID"
							value={workflow}
							onChange={(e) => setWorkflow(e.target.value)}
							className="max-w-xs"
						/>
						<Btn onClick={collection.reload} disabled={collection.loading}>
							Refresh
						</Btn>
						<Btn
							onClick={() => {
								setNote("");
								setSessionForm(true);
							}}
						>
							Session assignment
						</Btn>
						<Btn
							tone="primary"
							onClick={() => {
								setNote("");
								setEditing(null);
							}}
						>
							New workstream
						</Btn>
					</div>
					<p className="px-4 py-2 text-xs text-fg-faint">
						Tether returns up to the newest 100 matching workstreams. Search
						applies to this loaded set.
						{!collection.loading &&
						!collection.error &&
						collection.rows.length >= 100
							? " Older records may be omitted; narrow by status or workflow ID."
							: ""}
					</p>
					<Collection
						loading={collection.loading}
						error={collection.error}
						empty={!rows.length}
					>
						<ul aria-label="Workstreams">
							{rows.map((w) => (
								<li key={w.id}>
									<button
										type="button"
										className="flex w-full items-center gap-4 border-b border-border/80 px-4 py-4 text-left hover:bg-panel/60  "
										onClick={() => {
											setNote("");
											setEditing(w);
										}}
									>
										<span className="min-w-0 flex-1">
											<span className="block font-mono text-sm text-fg">
												{w.name || "Unnamed workstream"}
											</span>
											<span className="mt-1 block break-all font-mono text-xs text-fg-faint">
												{w.id}
											</span>
											{w.workflow_id && (
												<span className="mt-1 block text-xs text-fg-muted">
													Workflow: {w.workflow_id}
												</span>
											)}
										</span>
										<Chip>{w.status}</Chip>
									</button>
								</li>
							))}
						</ul>
					</Collection>
				</>
			)}
		</div>
	);
}

function WorkstreamEditor({
	workstream,
	client,
	onCancel,
	onSaved,
}: {
	workstream: Workstream | null;
	client: Client;
	onCancel(): void;
	onSaved(w: Workstream): void;
}) {
	const [name, setName] = useState(workstream?.name ?? "");
	const [workflow, setWorkflow] = useState(workstream?.workflow_id ?? "");
	const [status, setStatus] = useState<"active" | "closed">(
		workstream?.status ?? "active",
	);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	return (
		<form
			aria-label="Workstream editor"
			onSubmit={async (e) => {
				e.preventDefault();
				setBusy(true);
				setError("");
				try {
					onSaved(
						await client.saveWorkstream(
							{ name: name.trim(), workflow_id: workflow.trim(), status },
							workstream?.id,
						),
					);
				} catch (e) {
					setError(message(e));
				} finally {
					setBusy(false);
				}
			}}
		>
			<Notice error={error} />
			<fieldset disabled={busy} className="space-y-4 p-5">
				<div className="flex items-center gap-3">
					<h2 className="font-mono text-sm uppercase">
						{workstream ? "Edit workstream" : "New workstream"}
					</h2>
					<Btn className="ml-auto" onClick={onCancel}>
						Back to workstreams
					</Btn>
				</div>
				{workstream && (
					<p className="break-all font-mono text-xs text-fg-faint">
						{workstream.id}
					</p>
				)}
				<Field label="Name (optional)" htmlFor="workstream-name">
					<AdminInput
						id="workstream-name"
						className={inputCls}
						value={name}
						onChange={(e) => setName(e.target.value)}
					/>
				</Field>
				<Field label="Workflow ID (optional)" htmlFor="workstream-workflow">
					<AdminInput
						id="workstream-workflow"
						className={inputCls}
						value={workflow}
						onChange={(e) => setWorkflow(e.target.value)}
					/>
				</Field>
				<p className="text-xs text-fg-faint">
					A correlation identifier for a workflow owned by another system.
				</p>
				{workstream && (
					<Field label="Status" htmlFor="workstream-status">
						<AdminSelect
							id="workstream-status"
							className={inputCls}
							value={status}
							onChange={(e) => setStatus(e.target.value as "active" | "closed")}
						>
							<option value="active">Active</option>
							<option value="closed">Closed</option>
						</AdminSelect>
					</Field>
				)}
				<Btn type="submit" tone="primary" disabled={busy}>
					{busy
						? "Saving…"
						: workstream
							? "Save workstream"
							: "Create workstream"}
				</Btn>
			</fieldset>
		</form>
	);
}

function SessionWorkstreamForm({
	client,
	onCancel,
	onSaved,
}: {
	client: Client;
	onCancel(): void;
	onSaved(note: string): void;
}) {
	const [session, setSession] = useState("");
	const [action, setAction] = useState("ensure");
	const [id, setID] = useState("");
	const [name, setName] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	return (
		<form
			aria-label="Session workstream assignment"
			onSubmit={async (e) => {
				e.preventDefault();
				setBusy(true);
				setError("");
				try {
					const result = await client.sessionWorkstream(
						session.trim(),
						action === "ensure"
							? { ensure: true, name: name.trim() }
							: { workstream_id: action === "clear" ? "" : id.trim() },
					);
					onSaved(
						result
							? `Session workstream: ${result.name || "Unnamed"} · ${result.id}`
							: action === "clear"
								? `Cleared workstream assignment for ${session.trim()}.`
								: `Assigned ${session.trim()} to ${id.trim()}.`,
					);
				} catch (e) {
					setError(message(e));
				} finally {
					setBusy(false);
				}
			}}
		>
			<Notice error={error} />
			<fieldset disabled={busy} className="space-y-4 p-5">
				<div className="flex items-center gap-3">
					<h2 className="font-mono text-sm uppercase">Session workstream</h2>
					<Btn className="ml-auto" onClick={onCancel}>
						Back to workstreams
					</Btn>
				</div>
				<Field label="Session ID" htmlFor="session-id">
					<AdminInput
						id="session-id"
						required
						className={inputCls}
						value={session}
						onChange={(e) => setSession(e.target.value)}
					/>
				</Field>
				<Field label="Action" htmlFor="session-action">
					<AdminSelect
						id="session-action"
						className={inputCls}
						value={action}
						onChange={(e) => setAction(e.target.value)}
					>
						<option value="ensure">Find or create for this lineage</option>
						<option value="assign">Assign an existing workstream</option>
						<option value="clear">Clear this session’s assignment</option>
					</AdminSelect>
				</Field>
				{action === "ensure" && (
					<>
						<p className="text-sm text-fg-muted">
							Reuse the nearest workstream in the session’s lineage. If none
							exists, Tether creates one and fills missing assignments along
							that lineage.
						</p>
						<Field label="Name if created (optional)" htmlFor="ensure-name">
							<AdminInput
								id="ensure-name"
								className={inputCls}
								value={name}
								onChange={(e) => setName(e.target.value)}
							/>
						</Field>
					</>
				)}
				{action === "assign" && (
					<Field label="Workstream ID" htmlFor="assign-workstream">
						<AdminInput
							id="assign-workstream"
							required
							className={inputCls}
							value={id}
							onChange={(e) => setID(e.target.value)}
						/>
					</Field>
				)}
				{action === "clear" && (
					<p className="text-sm text-warning">
						Clears only this session’s association. The workstream and other
						sessions remain intact.
					</p>
				)}
				<Btn
					type="submit"
					tone={action === "clear" ? "danger" : "primary"}
					disabled={
						busy || !session.trim() || (action === "assign" && !id.trim())
					}
				>
					{busy
						? "Applying…"
						: action === "ensure"
							? "Find or create workstream"
							: action === "clear"
								? "Clear assignment"
								: "Assign workstream"}
				</Btn>
			</fieldset>
		</form>
	);
}
