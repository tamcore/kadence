<script lang="ts">
	import { onDestroy } from 'svelte';
	import { discardScheduledDraft } from '$lib/api/scheduled';
	import ScheduledProposal from '$lib/components/scheduled/ScheduledProposal.svelte';
	import ScheduledQuestionCard from '$lib/components/scheduled/ScheduledQuestionCard.svelte';
	import { ScheduledDefinitionController } from '$lib/scheduled/definition.svelte';
	import type { ScheduledArtifact } from '$lib/types';

	let { artifact, disabled = false }: { artifact: ScheduledArtifact; disabled?: boolean } = $props();
	const instanceID = $props.id();
	const headingID = `scheduled-work-order-${instanceID}`;
	const adjustID = `scheduled-adjust-${instanceID}`;
	let controller = new ScheduledDefinitionController(null);
	let mutating = $state(false);
	let dismissed = $state(false);
	let adjusting = $state(false);
	let adjustment = $state('');

	onDestroy(() => controller.dispose());
	$effect(() => {
		if (controller.taskId === null && artifact.taskId) controller.taskId = artifact.taskId;
	});

	const taskID = $derived(controller.taskId ?? artifact.taskId);
	const taskState = $derived(controller.task?.state ?? artifact.taskState);
	const question = $derived(controller.question ?? artifact.question);
	const proposal = $derived(controller.proposal ?? artifact.proposal);
	const isDismissed = $derived(dismissed || artifact.artifactState === 'dismissed');
	const pending = $derived(disabled || mutating || controller.sending);
	const canAdjust = $derived(
		taskID !== undefined && taskState === 'draft' && !isDismissed && !question && !proposal
	);

	function stateLabel(): string {
		if (isDismissed) return 'Dismissed';
		if (artifact.artifactState === 'creating') return 'Preparing delegated task';
		if (artifact.artifactState === 'failed') return artifact.retryable ? 'Ready to retry' : 'Could not prepare task';
		switch (taskState) {
			case 'active':
				return 'Scheduled';
			case 'paused':
				return 'Paused';
			case 'completed':
				return 'Completed';
			case 'failed':
				return 'Failed';
			case 'deleted':
				return 'Deleted';
			default:
				return proposal ? 'Ready to schedule' : question ? 'Needs your input' : 'Draft work order';
		}
	}

	function detailHref(): string | undefined {
		if (!taskID) return undefined;
		return taskState === 'draft' ? `/scheduled?task=${encodeURIComponent(taskID)}` : `/scheduled/${encodeURIComponent(taskID)}`;
	}

	async function answer(value: string): Promise<void> {
		await controller.answerQuestion(value);
	}

	async function confirm(version: number): Promise<void> {
		await controller.confirm(version);
	}

	async function dismiss(): Promise<void> {
		if (!taskID || taskState !== 'draft' || pending) return;
		mutating = true;
		try {
			await discardScheduledDraft(taskID);
			dismissed = true;
			controller.reset();
		} catch (cause) {
			controller.error = cause instanceof Error ? cause.message : 'Could not dismiss this draft';
		} finally {
			mutating = false;
		}
	}

	async function refine(message: string): Promise<void> {
		if (!taskID || !message.trim() || pending) return;
		await controller.refine(message.trim());
		adjustment = '';
		adjusting = false;
	}

	async function retry(): Promise<void> {
		if (!taskID || pending) return;
		await controller.refine('Please retry preparing this delegated task.');
	}
</script>

<section class="scheduled-artifact" aria-labelledby={headingID} data-handoff-id={artifact.handoffId}>
	<div class="scheduled-rail" aria-hidden="true"><span>{artifact.ordinal}</span></div>
	<div class="scheduled-content">
		<header class="scheduled-heading">
			<p class="scheduled-kicker">Delegated work order</p>
			<h3 id={headingID}>{proposal?.name ?? 'Scheduled follow-up'}</h3>
			<p class="scheduled-state" role="status" aria-live="polite">{stateLabel()}</p>
		</header>

		{#if controller.error}
			<p class="scheduled-error" role="status" aria-live="polite">{controller.error}</p>
		{/if}

		{#if !isDismissed && artifact.artifactState === 'failed' && artifact.retryable}
			<p class="scheduled-error" role="alert">This delegated task could not be prepared.</p>
		{/if}

		{#if !isDismissed && question}
			{#key question.id}
				<ScheduledQuestionCard
					{question}
					disabled={pending}
					autofocus={false}
					onAnswer={(value) => void answer(value)}
					onBack={controller.showPreviousQuestion}
				/>
			{/key}
		{:else if !isDismissed && proposal}
			<ScheduledProposal {proposal} disabled={pending} onConfirm={(version) => void confirm(version)} />
		{:else if controller.sending}
			<p class="scheduled-progress" aria-live="polite">Updating this work order…</p>
		{/if}

		{#if adjusting}
			<form
				class="scheduled-adjust"
				onsubmit={(event) => {
					event.preventDefault();
					void refine(adjustment);
				}}
			>
				<label for={adjustID}>Adjust scheduled task</label>
				<textarea id={adjustID} bind:value={adjustment} disabled={pending} rows="2"></textarea>
				<div class="scheduled-actions">
					<button type="button" class="secondary" disabled={pending} onclick={() => (adjusting = false)}
						>Cancel</button
					>
					<button type="submit" disabled={pending || !adjustment.trim()}>Save adjustment</button>
				</div>
			</form>
		{/if}

		<div class="scheduled-actions">
			{#if canAdjust && !adjusting}
				<button class="secondary" disabled={pending} onclick={() => (adjusting = true)}>Adjust</button>
			{/if}
			{#if !isDismissed && artifact.artifactState === 'failed' && artifact.retryable}
				<button disabled={pending} onclick={() => void retry()}>Retry</button>
			{/if}
			{#if !isDismissed && taskState === 'draft' && !question && !proposal}
				<button class="secondary danger" disabled={pending} onclick={() => void dismiss()}>Dismiss draft</button>
			{/if}
			{#if detailHref()}
				<a href={detailHref()}>Scheduled details</a>
			{/if}
		</div>
	</div>
</section>
