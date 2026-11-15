import { parseDocument } from "yaml";

export interface DefinitionPin {
	id: string;
	revision: string;
	digest: string;
}
export interface TeamBinding {
	launch: string;
	runtime: string;
	model?: string;
	definition: DefinitionPin;
	source?: string;
}
export interface TeamActivationPolicy {
	version: number;
	sessionFormation: boolean;
	adoption: boolean;
	bindings: TeamBinding[];
}

/** Read the authored activation policy, not a claim about daemon activation. */
export function parseTeamPolicy(content: string): TeamActivationPolicy {
	const doc = parseDocument(content, { uniqueKeys: true });
	if (doc.errors.length)
		throw new Error("Tether returned an invalid team activation policy.");
	const value = doc.toJS({ maxAliasCount: 50 });
	if (value?.version !== 1 || !Array.isArray(value.launches))
		throw new Error("Unsupported team activation policy format.");
	const bindings: TeamBinding[] = value.launches.map((b: TeamBinding) => {
		if (
			!b ||
			typeof b.launch !== "string" ||
			!b.launch ||
			typeof b.runtime !== "string" ||
			!b.runtime ||
			typeof b.definition?.id !== "string" ||
			!b.definition.id ||
			typeof b.definition.revision !== "string" ||
			!b.definition.revision ||
			typeof b.definition.digest !== "string" ||
			!/^sha256:[a-f0-9]{64}$/.test(b.definition.digest)
		)
			throw new Error(
				"Team activation policy contains an invalid launch binding.",
			);
		return {
			launch: b.launch,
			runtime: b.runtime,
			model: b.model,
			definition: { ...b.definition },
			source: b.source,
		};
	});
	if (new Set(bindings.map((b) => b.launch)).size !== bindings.length)
		throw new Error("Team activation policy contains duplicate launches.");
	return {
		version: value.version,
		sessionFormation: value.session_formation === true,
		adoption: value.adoption === true,
		bindings,
	};
}

export interface SlotDraft {
	name: string;
	role: string;
	launch: string;
	min: number;
	max: number;
}
export interface TeamConfigDraft {
	key: string;
	id: string;
	name: string;
	version: number;
	slots: SlotDraft[];
	coordinator: string;
	history: "none" | "summary" | "full";
	maxChildren: number;
	maxDepth: number;
}
export function newTeamDraft(): TeamConfigDraft {
	return {
		key: crypto.randomUUID(),
		id: "",
		name: "",
		version: 1,
		slots: [
			{ name: "coordinator", role: "coordinator", launch: "", min: 0, max: 1 },
			{ name: "workers", role: "worker", launch: "", min: 1, max: 1 },
			{ name: "reviewer", role: "reviewer", launch: "", min: 1, max: 1 },
		],
		coordinator: "coordinator",
		history: "none",
		maxChildren: 4,
		maxDepth: 1,
	};
}
export function teamDraftIssues(
	d: TeamConfigDraft,
	policy: TeamActivationPolicy | null,
): string[] {
	const issues: string[] = [];
	const id = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/;
	if (!id.test(d.id))
		issues.push(
			"Team ID must use letters, numbers, dots, underscores or hyphens.",
		);
	if (!d.name.trim()) issues.push("Give the team a name.");
	if (!Number.isSafeInteger(d.version) || d.version < 1)
		issues.push("Version must be a positive whole number.");
	if (!d.slots.length) issues.push("Add at least one slot.");
	if (new Set(d.slots.map((s) => s.name)).size !== d.slots.length)
		issues.push("Slot names must be unique.");
	for (const s of d.slots) {
		if (!id.test(s.name) || ["self", "all"].includes(s.name))
			issues.push("Each slot needs a valid, non-reserved name.");
		if (!s.role.trim()) issues.push(`${s.name || "Slot"} needs a role.`);
		if (!policy?.bindings.some((b) => b.launch === s.launch))
			issues.push(
				`${s.name || "Slot"} needs a launch from the activation policy.`,
			);
		if (![0, 1].includes(s.min) || s.max !== 1)
			issues.push(
				`${s.name || "Slot"} must be an optional or required single seat. Concurrent seats require enrolled worker pools.`,
			);
	}
	if (!d.slots.some((s) => s.name === d.coordinator))
		issues.push("Choose an existing coordinator slot.");
	if (
		![d.maxChildren, d.maxDepth].every(
			(n) => Number.isSafeInteger(n) && n >= 1 && n <= 100,
		)
	)
		issues.push("Spawn limits must be whole numbers between 1 and 100.");
	if (!policy)
		issues.push("Load the Tether activation policy before reviewing a draft.");
	return issues;
}

/** Preview only. Tether still owns pin verification, authorization and launch validation. */
export function teamPreview(d: TeamConfigDraft, policy: TeamActivationPolicy) {
	return {
		id: d.id,
		name: d.name.trim(),
		version: d.version,
		slots: d.slots.map((s) => ({
			name: s.name,
			role: s.role,
			definition_ref: policy.bindings.find((b) => b.launch === s.launch)!
				.definition,
			resolution: "fresh",
			activation: "singleton",
			required: s.min > 0,
			min: s.min,
			max: s.max,
		})),
		phases: [
			{
				id: "work",
				kind: "flex",
				active_slots: d.slots.map((s) => s.name),
				owner_slot: d.coordinator,
				exit_trigger: { kind: "manual", spec: { signal: "done" } },
			},
		],
		authority: {
			mode: "strict",
			grants: d.slots
				.filter((s) => s.name !== d.coordinator)
				.flatMap((s) =>
					["may_message", "may_assign"].map((verb) => ({
						from_slot: d.coordinator,
						verb,
						to_slot: s.name,
					})),
				),
		},
		routing: { coordinator_slot: d.coordinator, rules: [] },
		policy: {
			history: d.history,
			spawn: { max_depth: d.maxDepth, max_children: d.maxChildren },
		},
	};
}
