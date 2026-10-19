import { AdminDirectory, AdminRecordButton } from '@hollis-labs/kit-admin'
import { AdminInput, AdminSelect } from '@hollis-labs/kit-admin'
import { useCallback, useEffect, useRef, useState } from "react";
import { dataRefreshRegistry } from "@tachyon/host-sdk";
import { EmptyState } from "@hollis-labs/design-components";
import { Field, inputCls } from "./AgentEditor";
import { useAgentsClient } from "./context";
import { teamDraftFromRecord, type OwnedTeam, type TeamPage } from './owned-teams';
import { Block, Btn } from "./ui";
import { TeamFormation } from "./TeamFormation";
import { ImportedTeamDetail, importedTeamKey, type ImportedTeamSnapshot } from './ImportedTeamDetail';
import {
	newTeamDraft,
	teamDraftIssues,
	teamPreview,
	type TeamActivationPolicy,
	type TeamConfigDraft,
} from "./team-config";

/** Authored team workspace. Local drafts never represent live memberships. */
export function TeamBuilder() {
	const client = useAgentsClient();
	const store = client.ownedTeams;
	const [owned, setOwned] = useState<OwnedTeam[]>(() => store?.cached()?.items ?? []);
	const [next, setNext] = useState<string | undefined>(() => store?.cached()?.next_after_id);
	const [ownedError, setOwnedError] = useState<string | null>(null);
	const [writeError, setWriteError] = useState<string | null>(null);
	const [busy, setBusy] = useState(false);
	const [bases, setBases] = useState<Record<string, OwnedTeam>>({});
	const readVersion = useRef(0);
	const loading = useRef(false);
	const mounted = useRef(true);
	const loadedPages = useRef(new Map<string, TeamPage>());
	const loadOwned = useCallback(async (after = '') => {
		if (!store || loading.current) return;
		loading.current = true;
		const version = readVersion.current;
		try {
			const page = await store.list(after);
			if (!mounted.current || version !== readVersion.current) return;
			loadedPages.current.set(after, page);
			const pages = [...loadedPages.current.values()];
			setOwned([...new Map(pages.flatMap(p => p.items).map(row => [row.id, row])).values()]);
			setNext(pages.at(-1)?.next_after_id); setOwnedError(null);
		} catch (e) { if (mounted.current) setOwnedError(e instanceof Error ? e.message : 'Saved teams unavailable'); }
		finally { loading.current = false; }
	}, [store]);
	useEffect(() => {
		mounted.current = true;
		void loadOwned();
		const refresh = async () => { for (const cursor of loadedPages.current.size ? loadedPages.current.keys() : ['']) await loadOwned(cursor); };
		const off = dataRefreshRegistry('owned-teams').subscribe(() => void refresh());
		const timer = setInterval(() => void refresh(), 30_000);
		return () => { mounted.current = false; off(); clearInterval(timer); };
	}, [loadOwned]);
	const [policy, setPolicy] = useState<TeamActivationPolicy | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [drafts, setDrafts] = useState<TeamConfigDraft[]>([]);
	const [selected, setSelected] = useState<string | null>(null);
	const [review, setReview] = useState<ReturnType<typeof teamPreview> | null>(
		null,
	);
	const [message, setMessage] = useState<string | null>(null);
	const [search, setSearch] = useState("");
	const [snapshot, setSnapshot] = useState<ImportedTeamSnapshot | null>(null);
	const [snapshotError, setSnapshotError] = useState<string | null>(null);
	const loadSnapshot = useCallback(async () => {
		try {
			if (!client.getImportedTeams) return;
			const value = await client.getImportedTeams();
			setSnapshot(value); setSnapshotError(null);
		} catch (e) { setSnapshotError(e instanceof Error ? e.message : 'Imported teams unavailable'); }
	}, [client]);
	useEffect(() => { void loadSnapshot(); return dataRefreshRegistry('catalog').subscribe(() => void loadSnapshot()); }, [loadSnapshot]);
	const load = useCallback(async () => {
		try {
			if (!client.getTeamActivationPolicy)
				throw new Error("Team activation policy reads are not connected.");
			setPolicy(await client.getTeamActivationPolicy());
			setError(null);
		} catch (e) {
			setError(e instanceof Error ? e.message : "Team policy unavailable");
		}
	}, [client]);
	useEffect(() => {
		void load();
		return dataRefreshRegistry("catalog").subscribe(() => void load());
	}, [load]);
	const draft = drafts.find((d) => d.key === selected);
	const base = selected ? bases[selected] : undefined;
	const openOwned = async (id: string, reload = false) => {
		if (busy || !store) return;
		if (!reload && drafts.some(d => d.key === id)) { select(id); return; }
		setBusy(true);
		try {
			const row = await store.get(id);
			const value = teamDraftFromRecord(row);
			if (!value) throw new Error('This team uses another editor format. Its definition is retained unchanged.');
			setBases(b => ({ ...b, [id]: row }));
			setDrafts(ds => [...ds.filter(d => d.key !== id), value]);
			select(id); setOwnedError(null); setWriteError(null);
		} catch (e) { setOwnedError(e instanceof Error ? e.message : 'Saved team unavailable'); }
		finally { setBusy(false); }
	};
	const save = async (retire = false) => {
		if (!store || !draft || !review || busy) return;
		setBusy(true); setWriteError(null); readVersion.current++;
		try {
			const row = retire && base ? await store.retire(base, !!base.deleted_at) : await store.save(draft, review, base);
			readVersion.current++;
			setBases(b => ({ ...b, [row.id]: row }));
			setOwned(rows => [row, ...rows.filter(r => r.id !== row.id)]);
			for (const page of loadedPages.current.values()) page.items = page.items.map(item => item.id === row.id ? row : item);
			setMessage(retire ? row.deleted_at ? 'Team retired. Its history is retained.' : 'Team restored.' : 'Team definition saved in Tachyon.');
		} catch (e) { setWriteError(e instanceof Error ? e.message : 'Team save failed. Your draft is preserved.'); }
		finally { setBusy(false); }
	};
	const savedTeams = [...(snapshot?.teams ?? [])].sort((a, b) => a.id.localeCompare(b.id) || b.version - a.version);
	const imported = savedTeams.find(t => importedTeamKey(t) === selected) ?? (selected === null ? savedTeams[0] : undefined);
	const issues = draft ? teamDraftIssues(draft, policy) : [];
	if (draft && drafts.some((d) => d.key !== draft.key && d.id === draft.id))
		issues.push("Another local draft has this team ID.");
	const update = (patch: Partial<TeamConfigDraft>) => {
		setDrafts((ds) =>
			ds.map((d) => (d.key === selected ? { ...d, ...patch } : d)),
		);
		setReview(null);
		setMessage(null);
	};
	const create = () => {
		const d = newTeamDraft();
		setDrafts((ds) => [...ds, d]);
		setSelected(d.key);
		setReview(null);
		setMessage(null);
	};
	const select = (key: string) => {
		if (busy) return;
		setSelected(key);
		setReview(null);
		setMessage(null);
	};
	return (
		<div>
			<div className="flex flex-wrap items-center gap-3 border-b border-border/80 px-4 py-3">
				<div className="min-w-0 flex-1">
					<h2 className="font-mono dash-body font-semibold uppercase tracking-wide">
						Team configuration
					</h2>
					<p className="mt-1 dash-support text-fg-muted">
						Define seats, assign pinned launch profiles, and configure shared
						policy.
					</p>
				</div>
				<Btn tone="primary" onClick={create} disabled={busy}>
					New team draft
				</Btn>
			</div>
			<p className="border-b border-border/80 px-4 py-2 dash-support text-fg-muted">
				Save team definitions in Tachyon after reviewing them. Unsaved edits stay in this workspace.
				Launching a team is a separate action. Imported snapshots remain read-only.
			</p>
			{ownedError && <div role="alert" className="border-b border-danger/30 px-4 py-3 dash-body text-danger">{ownedError} <Btn disabled={busy} onClick={() => void loadOwned()}>Refresh saved teams</Btn></div>}
			{writeError && <div role="alert" className="border-b border-danger/30 px-4 py-3 dash-body text-danger">{writeError}</div>}
			{error && (
				<div
					role="alert"
					className="flex flex-wrap items-center gap-3 border-b border-danger/30 bg-danger/5 px-4 py-3 dash-body text-danger"
				>
					{error}
					<Btn onClick={() => void load()}>Retry team policy</Btn>
				</div>
			)}
			<AdminDirectory label="Team configuration" layout="compact" records={
				<aside>
					<div className="border-b border-border/80 px-4 py-3"><h3 className="font-mono dash-label uppercase tracking-widest text-fg-muted">Tachyon team definitions</h3></div>
					<ul aria-label="Tachyon teams">{owned.map(row => <li key={row.id}><AdminRecordButton selected={selected === row.id} onSelect={() => void openOwned(row.id)} size="comfortable" title={row.name} description={`Revision ${row.revision}${row.deleted_at ? ' · retired' : ''}${row.ownership !== 'tachyon' ? ' · provider observation' : ''}`} /></li>)}</ul>
					{next && <Btn disabled={busy} onClick={() => void loadOwned(next)}>Load more saved teams</Btn>}
					<div className="border-b border-border/80 px-4 py-3"><h3 className="font-mono dash-label uppercase tracking-widest text-fg-muted">Saved team snapshots</h3><p className="mt-1 dash-support text-fg-faint">{snapshot ? `${new Set(savedTeams.map(t => t.id)).size} teams · ${savedTeams.length} versions · manually imported` : 'Loading local snapshot…'}</p>
					{snapshotError && <p role="alert" className="mt-2 dash-support text-danger">{snapshotError} <button type="button" className="underline" onClick={() => void loadSnapshot()}>Retry snapshot</button></p>}</div>
					<ul aria-label="Saved teams">{savedTeams.map(t => <li key={importedTeamKey(t)}><AdminRecordButton selected={imported === t} onSelect={() => select(importedTeamKey(t))} size="comfortable" title={t.id} description={`${t.name} · version ${t.version}`} /></li>)}</ul>
					{snapshot && !savedTeams.length && <p className="px-4 py-3 dash-support text-fg-faint">No saved team snapshot has been imported, or it has expired from the cache.</p>}
					<div className="border-b border-border/80 px-4 py-3">
						<h3 className="mb-2 font-mono dash-label uppercase tracking-widest text-fg-muted">
									Open drafts · {drafts.length}
						</h3>
						<AdminInput
							aria-label="Search team drafts"
							placeholder="Search drafts"
							value={search}
							onChange={(e) => setSearch(e.target.value)}
							className={inputCls}
						/>
					</div>
					<ul aria-label="Team drafts">
						{drafts
							.filter((d) =>
								`${d.name} ${d.id}`
									.toLowerCase()
									.includes(search.toLowerCase()),
							)
							.map((d) => (
								<li key={d.key}>
                  <AdminRecordButton selected={selected === d.key} onSelect={() => { if (!busy) select(d.key) }} size="comfortable" title={d.name || 'Untitled team'} description={`${d.slots.length} seats · ${bases[d.key] ? 'editing saved team' : 'unsaved draft'}`} />
								</li>
							))}
					</ul>
					{!drafts.length && (
						<p className="px-4 py-4 dash-body text-fg-faint">
							Create a draft to start configuring a team.
						</p>
					)}
					<div className="px-4 py-4">
						<h3 className="font-mono dash-label uppercase tracking-widest text-fg-muted">
							Tether launch bindings
						</h3>
						{policy ? (
							<>
								<p className="mt-2 dash-support text-fg-faint">
									{policy.bindings.length} bindings in the authored activation
									policy. Host activation may differ.
								</p>
								<ul className="mt-3 space-y-3">
									{policy.bindings.map((b) => (
										<li key={b.launch}>
											<span className="block break-all font-mono dash-support text-fg">
												{b.launch}
											</span>
											<span className="block dash-support text-fg-faint">
												{b.runtime}
												{b.model ? ` · ${b.model}` : ""} ·{" "}
												{b.definition.revision}
											</span>
										</li>
									))}
								</ul>
							</>
						) : (
							<p className="mt-2 dash-support text-fg-faint">
								{error ? "Policy unavailable." : "Loading policy…"}
							</p>
						)}
					</div>
				</aside>} detail={<>
				{imported && snapshot ? <ImportedTeamDetail team={imported} snapshot={snapshot} /> : !draft ? (
					<div className="p-6">
						<EmptyState
							variant="empty"
							title="Build a team"
							description="Start with coordinator, worker and reviewer seats. Choose each seat’s launch binding, then review the configuration without starting a run."
						/>
					</div>
				) : review ? (
					<article aria-label="Team draft review">
						<Block title="Review team draft">
							<h2 className="dash-detail-title font-semibold">{review.name}</h2>
							<p className="mt-1 dash-body text-fg-muted">
								{review.id} · version {review.version} · {review.slots.length}{" "}
								seats
							</p>
							<p className="mt-3 dash-body text-fg-muted">
								Save this configuration in Tachyon. Tether verifies definition pins,
								host policy and launch eligibility when a team is deployed.
							</p>
						</Block>
						<Block title="Seat assignments">
							<ul className="space-y-3">
								{review.slots.map((s) => (
									<li key={s.name} className="dash-body">
										<span className="font-mono text-fg">{s.name}</span>
										<span className="text-fg-muted">
											{" "}
											· {s.role} · {s.definition_ref.id}@
											{s.definition_ref.revision} ·{" "}
											{s.required ? "required" : "optional"}
										</span>
									</li>
								))}
							</ul>
						</Block>
						<Block title="Shared policy">
							<p className="dash-body text-fg-muted">
								Strict authority · coordinator {review.routing.coordinator_slot}{" "}
								· history {review.policy.history}
							</p>
							<p className="mt-2 dash-body text-fg-muted">
								Coordinator may message and assign to the other seats. Spawn
								depth {review.policy.spawn.max_depth}, children{" "}
								{review.policy.spawn.max_children}. These limits do not grant
								launch permission.
							</p>
						</Block>
						<Block title="Configuration preview">
							<details>
								<summary className="cursor-pointer dash-body text-fg-muted">
									View complete draft
								</summary>
								<pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all dash-support text-fg-muted">
									{JSON.stringify(review, null, 2)}
								</pre>
							</details>
						</Block>
						{message && (
							<div
								role="status"
								className="border-b border-success/30 bg-success/5 px-5 py-3 dash-body text-success"
							>
								{message}
							</div>
						)}
						<div className="flex gap-3 px-5 py-4">
							<Btn
								disabled={busy}
								onClick={() => {
									setReview(null);
									setMessage(null);
								}}
							>
								{message ? "Back to draft" : "Continue editing"}
							</Btn>
							<Btn
								tone="primary"
								disabled={busy || !!message || !store || !!base?.deleted_at}
								onClick={() => void save()}
							>
								{busy ? 'Saving…' : 'Save team definition'}
							</Btn>
							{(base || writeError) && <Btn disabled={busy} onClick={() => void openOwned(draft.key, true)}>Discard draft and reload saved version</Btn>}
							{base && <Btn disabled={busy || !!message} onClick={() => void save(true)}>{base.deleted_at ? 'Restore team definition' : 'Retire team definition'}</Btn>}
						</div>
					</article>
				) : (
					<article aria-label="Configure team draft">
						<Block title="Team definition">
							<div className="grid gap-3 sm:grid-cols-2">
								<Field label="Team name" htmlFor="team-name">
									<AdminInput
										id="team-name"
										value={draft.name}
										onChange={(e) =>
											update({
												name: e.target.value,
												...(!draft.id ||
												draft.id ===
													draft.name
														.toLowerCase()
														.replace(/[^a-z0-9]+/g, "-")
														.replace(/^-|-$/g, "")
													? {
															id: e.target.value
																.toLowerCase()
																.replace(/[^a-z0-9]+/g, "-")
																.replace(/^-|-$/g, ""),
														}
													: {}),
											})
										}
										className={inputCls}
									/>
								</Field>
								<Field label="Team ID" htmlFor="team-id">
									<AdminInput
										id="team-id"
										value={draft.id}
										onChange={(e) => update({ id: e.target.value })}
										className={inputCls}
									/>
								</Field>
								<Field label="Version" htmlFor="team-version">
									<AdminInput
										id="team-version"
										type="number"
										min="1"
										value={draft.version}
										onChange={(e) =>
											update({ version: Number(e.target.value) })
										}
										className={inputCls}
									/>
								</Field>
								<Field label="Coordinator slot" htmlFor="team-coordinator">
									<AdminSelect
										id="team-coordinator"
										value={draft.coordinator}
										onChange={(e) => update({ coordinator: e.target.value })}
										className={inputCls}
									>
										<option value="">Choose a slot…</option>
										{draft.slots.map((s, i) => (
											<option key={i} value={s.name}>
												{s.name || "(unnamed)"}
											</option>
										))}
									</AdminSelect>
								</Field>
							</div>
						</Block>
						<Block title="Seats and assignments">
							<p className="mb-3 dash-body text-fg-muted">
								Each seat uses one fresh agent from a pinned definition. Runtime
								instances are managed on Agents.
							</p>
							<div className="space-y-4">
								{draft.slots.map((s, i) => {
									const patch = (p: Partial<typeof s>) =>
										update({
											slots: draft.slots.map((v, n) =>
												n === i ? { ...v, ...p } : v,
											),
										});
									const binding = policy?.bindings.find(
										(b) => b.launch === s.launch,
									);
									return (
										<fieldset
											key={i}
											className="min-w-0 border border-border p-3"
										>
											<legend className="px-1 font-mono dash-support text-fg-muted">
												Seat {i + 1}
											</legend>
											<div className="grid gap-3 sm:grid-cols-2">
												<Field label="Slot name" htmlFor={`slot-${i}`}>
													<AdminInput
														id={`slot-${i}`}
														value={s.name}
														onChange={(e) => patch({ name: e.target.value })}
														className={inputCls}
													/>
												</Field>
												<Field label="Role" htmlFor={`role-${i}`}>
													<AdminInput
														id={`role-${i}`}
														value={s.role}
														onChange={(e) => patch({ role: e.target.value })}
														className={inputCls}
													/>
												</Field>
												<div className="sm:col-span-2">
													<Field label="Launch binding" htmlFor={`launch-${i}`}>
														<AdminSelect
															id={`launch-${i}`}
															value={s.launch}
															onChange={(e) =>
																patch({ launch: e.target.value })
															}
															className={inputCls}
														>
															<option value="">
																Choose from Tether policy…
															</option>
															{s.launch && !binding && (
																<option value={s.launch}>
																	{s.launch} (no longer available)
																</option>
															)}
															{policy?.bindings.map((b) => (
																<option key={b.launch} value={b.launch}>
																	{b.launch} · {b.runtime} · {b.definition.id}
																</option>
															))}
														</AdminSelect>
													</Field>
												</div>
											</div>
											{binding && (
												<p
													className="mt-2 break-all font-mono dash-label text-fg-faint"
													title={binding.definition.digest}
												>
													{binding.definition.id}@{binding.definition.revision}{" "}
													· {binding.definition.digest.slice(0, 22)}…
												</p>
											)}
											<div className="mt-3 flex items-center gap-3">
												<label className="flex items-center gap-2 dash-support text-fg-muted">
													<input
														type="checkbox"
														checked={s.min === 1}
														onChange={(e) =>
															patch({ min: e.target.checked ? 1 : 0 })
														}
													/>
													Required seat
												</label>
												<Btn
													className="ml-auto"
													onClick={() =>
														update({
															slots: draft.slots.filter((_, n) => n !== i),
														})
													}
													aria-label={`Remove seat ${i + 1}`}
												>
													Remove
												</Btn>
											</div>
										</fieldset>
									);
								})}
							</div>
							<Btn
								className="mt-3"
								onClick={() =>
									update({
										slots: [
											...draft.slots,
											{
												name: `seat-${draft.slots.length + 1}`,
												role: "worker",
												launch: "",
												min: 1,
												max: 1,
											},
										],
									})
								}
							>
								Add seat
							</Btn>
						</Block>
						<Block title="Shared policy">
							<div className="grid gap-3 sm:grid-cols-3">
								<Field label="History sharing" htmlFor="team-history">
									<AdminSelect
										id="team-history"
										value={draft.history}
										onChange={(e) =>
											update({
												history: e.target.value as TeamConfigDraft["history"],
											})
										}
										className={inputCls}
									>
										<option value="none">None</option>
										<option value="summary">Summary</option>
										<option value="full">Full</option>
									</AdminSelect>
								</Field>
								<Field label="Maximum depth" htmlFor="team-depth">
									<AdminInput
										id="team-depth"
										type="number"
										min="1"
										max="100"
										value={draft.maxDepth}
										onChange={(e) =>
											update({ maxDepth: Number(e.target.value) })
										}
										className={inputCls}
									/>
								</Field>
								<Field label="Maximum children" htmlFor="team-children">
									<AdminInput
										id="team-children"
										type="number"
										min="1"
										max="100"
										value={draft.maxChildren}
										onChange={(e) =>
											update({ maxChildren: Number(e.target.value) })
										}
										className={inputCls}
									/>
								</Field>
							</div>
							<p className="mt-3 dash-support text-fg-faint">
								Strict authority: the coordinator may message and assign to
								other seats. Host policy remains the authority for effective
								permissions.
							</p>
						</Block>
						<div className="px-5 py-3">
							{issues.length > 0 && (
								<ul
									aria-label="Draft validation"
									className="mb-3 space-y-1 dash-support text-warning"
								>
									{issues.map((issue, i) => (
										<li key={i}>{issue}</li>
									))}
								</ul>
							)}
							<div className="flex flex-wrap gap-2">
								<Btn
									onClick={() => {
										const copy = {
											...structuredClone(draft),
											key: crypto.randomUUID(),
											id: `${draft.id}-copy`,
											name: `${draft.name || "Team"} copy`,
										};
										setDrafts((ds) => [...ds, copy]);
										select(copy.key);
									}}
								>
									Duplicate draft
								</Btn>
								<Btn
									onClick={() => {
										setDrafts((ds) => ds.filter((d) => d.key !== selected));
										setSelected(null);
									}}
								>
									Discard draft
								</Btn>
								<Btn
									className="ml-auto"
									tone="primary"
									disabled={issues.length > 0 || !!error}
									onClick={() =>
										policy && setReview(teamPreview(draft, policy))
									}
								>
									Review team draft
								</Btn>
							</div>
						</div>
					</article>
				)}
                {base && <TeamFormation key={base.id} team={base} />}
			</>} />
		</div>
	);
}
