import { useCallback, useEffect, useState } from "react";
import { dataRefreshRegistry } from "@tachyon/host-sdk";
import { EmptyState } from "@hollis-labs/design-components";
import { Field, inputCls } from "./AgentEditor";
import { useAgentsClient } from "./context";
import { DEMO_SAVE_MESSAGE } from "./catalog-client";
import { Block, Btn } from "./ui";
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
		setSelected(key);
		setReview(null);
		setMessage(null);
	};
	return (
		<div>
			<div className="flex flex-wrap items-center gap-3 border-b border-zinc-800/80 px-4 py-3">
				<div className="min-w-0 flex-1">
					<h2 className="font-mono text-sm font-semibold uppercase tracking-wide">
						Team configuration
					</h2>
					<p className="mt-1 text-xs text-zinc-400">
						Define seats, assign pinned launch profiles, and configure shared
						policy.
					</p>
				</div>
				<Btn tone="primary" onClick={create}>
					New team draft
				</Btn>
			</div>
			<p className="border-b border-zinc-800/80 px-4 py-2 text-xs text-zinc-400">
				Team drafts stay in this Builder workspace until you leave or reload.
				Imported saved teams are read-only snapshots; live memberships are not connected here yet.
			</p>
			{error && (
				<div
					role="alert"
					className="flex flex-wrap items-center gap-3 border-b border-rose-400/30 bg-rose-400/5 px-4 py-3 text-sm text-rose-200"
				>
					{error}
					<Btn onClick={() => void load()}>Retry team policy</Btn>
				</div>
			)}
			<div className="grid lg:grid-cols-[minmax(15rem,21rem)_minmax(0,1fr)]">
				<aside className="lg:border-r lg:border-zinc-800/80">
					<div className="border-b border-zinc-800/80 px-4 py-3"><h3 className="font-mono text-[11px] uppercase tracking-widest text-zinc-400">Saved team snapshots</h3><p className="mt-1 text-xs text-zinc-500">{snapshot ? `${new Set(savedTeams.map(t => t.id)).size} teams · ${savedTeams.length} versions · manually imported` : 'Loading local snapshot…'}</p>
					{snapshotError && <p role="alert" className="mt-2 text-xs text-rose-300">{snapshotError} <button type="button" className="underline" onClick={() => void loadSnapshot()}>Retry snapshot</button></p>}</div>
					<ul aria-label="Saved teams">{savedTeams.map(t => <li key={importedTeamKey(t)}><button type="button" aria-current={imported === t ? 'true' : undefined} onClick={() => select(importedTeamKey(t))} className={`w-full border-b border-zinc-800/80 px-4 py-3 text-left focus-visible:ring-1 focus-visible:ring-amber-300 ${imported === t ? 'bg-zinc-900' : 'hover:bg-zinc-900/50'}`}><span className="block font-mono text-sm text-zinc-100">{t.id}</span><span className="mt-1 block text-xs text-zinc-400">{t.name} · version {t.version}</span></button></li>)}</ul>
					{snapshot && !savedTeams.length && <p className="px-4 py-3 text-xs text-zinc-500">No saved team snapshot has been imported, or it has expired from the cache.</p>}
					<div className="border-b border-zinc-800/80 px-4 py-3">
						<h3 className="mb-2 font-mono text-[11px] uppercase tracking-widest text-zinc-400">
							Local drafts · {drafts.length}
						</h3>
						<input
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
									<button
										type="button"
										aria-current={selected === d.key ? "true" : undefined}
										onClick={() => select(d.key)}
										className={`w-full border-b border-zinc-800/80 px-4 py-3 text-left focus-visible:ring-1 focus-visible:ring-amber-300 ${selected === d.key ? "bg-zinc-900" : "hover:bg-zinc-900/50"}`}
									>
										<span className="block font-mono text-sm text-zinc-100">
											{d.name || "Untitled team"}
										</span>
										<span className="mt-1 block text-xs text-zinc-400">
											{d.slots.length} seats · unsaved draft
										</span>
									</button>
								</li>
							))}
					</ul>
					{!drafts.length && (
						<p className="px-4 py-4 text-sm text-zinc-500">
							Create a draft to start configuring a team.
						</p>
					)}
					<div className="px-4 py-4">
						<h3 className="font-mono text-[11px] uppercase tracking-widest text-zinc-400">
							Tether launch bindings
						</h3>
						{policy ? (
							<>
								<p className="mt-2 text-xs text-zinc-500">
									{policy.bindings.length} bindings in the authored activation
									policy. Host activation may differ.
								</p>
								<ul className="mt-3 space-y-3">
									{policy.bindings.map((b) => (
										<li key={b.launch}>
											<span className="block break-all font-mono text-xs text-zinc-200">
												{b.launch}
											</span>
											<span className="block text-xs text-zinc-500">
												{b.runtime}
												{b.model ? ` · ${b.model}` : ""} ·{" "}
												{b.definition.revision}
											</span>
										</li>
									))}
								</ul>
							</>
						) : (
							<p className="mt-2 text-xs text-zinc-500">
								{error ? "Policy unavailable." : "Loading policy…"}
							</p>
						)}
					</div>
				</aside>
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
							<h2 className="text-lg font-semibold">{review.name}</h2>
							<p className="mt-1 text-sm text-zinc-400">
								{review.id} · version {review.version} · {review.slots.length}{" "}
								seats
							</p>
							<p className="mt-3 text-sm text-zinc-400">
								This is a local configuration review. Tether must verify pins,
								host policy and launch eligibility before a future real save or
								run.
							</p>
						</Block>
						<Block title="Seat assignments">
							<ul className="space-y-3">
								{review.slots.map((s) => (
									<li key={s.name} className="text-sm">
										<span className="font-mono text-zinc-100">{s.name}</span>
										<span className="text-zinc-400">
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
							<p className="text-sm text-zinc-300">
								Strict authority · coordinator {review.routing.coordinator_slot}{" "}
								· history {review.policy.history}
							</p>
							<p className="mt-2 text-sm text-zinc-400">
								Coordinator may message and assign to the other seats. Spawn
								depth {review.policy.spawn.max_depth}, children{" "}
								{review.policy.spawn.max_children}. These limits do not grant
								launch permission.
							</p>
						</Block>
						<Block title="Configuration preview">
							<details>
								<summary className="cursor-pointer text-sm text-zinc-300">
									View complete draft
								</summary>
								<pre className="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs text-zinc-400">
									{JSON.stringify(review, null, 2)}
								</pre>
							</details>
						</Block>
						{message && (
							<div
								role="status"
								className="border-b border-emerald-400/30 bg-emerald-400/5 px-5 py-3 text-sm text-emerald-200"
							>
								{message}
							</div>
						)}
						<div className="flex gap-3 px-5 py-4">
							<Btn
								onClick={() => {
									setReview(null);
									setMessage(null);
								}}
							>
								{message ? "Back to draft" : "Continue editing"}
							</Btn>
							<Btn
								tone="primary"
								disabled={!!message}
								onClick={() => setMessage(DEMO_SAVE_MESSAGE)}
							>
								Save demonstration
							</Btn>
						</div>
					</article>
				) : (
					<article aria-label="Configure team draft">
						<Block title="Team definition">
							<div className="grid gap-3 sm:grid-cols-2">
								<Field label="Team name" htmlFor="team-name">
									<input
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
									<input
										id="team-id"
										value={draft.id}
										onChange={(e) => update({ id: e.target.value })}
										className={inputCls}
									/>
								</Field>
								<Field label="Version" htmlFor="team-version">
									<input
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
									<select
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
									</select>
								</Field>
							</div>
						</Block>
						<Block title="Seats and assignments">
							<p className="mb-3 text-sm text-zinc-400">
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
											className="min-w-0 border border-zinc-800 p-3"
										>
											<legend className="px-1 font-mono text-xs text-zinc-400">
												Seat {i + 1}
											</legend>
											<div className="grid gap-3 sm:grid-cols-2">
												<Field label="Slot name" htmlFor={`slot-${i}`}>
													<input
														id={`slot-${i}`}
														value={s.name}
														onChange={(e) => patch({ name: e.target.value })}
														className={inputCls}
													/>
												</Field>
												<Field label="Role" htmlFor={`role-${i}`}>
													<input
														id={`role-${i}`}
														value={s.role}
														onChange={(e) => patch({ role: e.target.value })}
														className={inputCls}
													/>
												</Field>
												<div className="sm:col-span-2">
													<Field label="Launch binding" htmlFor={`launch-${i}`}>
														<select
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
														</select>
													</Field>
												</div>
											</div>
											{binding && (
												<p
													className="mt-2 break-all font-mono text-[11px] text-zinc-500"
													title={binding.definition.digest}
												>
													{binding.definition.id}@{binding.definition.revision}{" "}
													· {binding.definition.digest.slice(0, 22)}…
												</p>
											)}
											<div className="mt-3 flex items-center gap-3">
												<label className="flex items-center gap-2 text-xs text-zinc-300">
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
									<select
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
									</select>
								</Field>
								<Field label="Maximum depth" htmlFor="team-depth">
									<input
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
									<input
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
							<p className="mt-3 text-xs text-zinc-500">
								Strict authority: the coordinator may message and assign to
								other seats. Host policy remains the authority for effective
								permissions.
							</p>
						</Block>
						<div className="px-5 py-3">
							{issues.length > 0 && (
								<ul
									aria-label="Draft validation"
									className="mb-3 space-y-1 text-xs text-amber-200"
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
			</div>
		</div>
	);
}
