<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { onDestroy, onMount } from 'svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import {
		deleteScheduledTask,
		getScheduledTask,
		markScheduledTaskRead,
		pauseScheduledTask,
		resumeScheduledTask,
		runScheduledTaskNow,
		type ScheduledDetail
	} from '$lib/api/scheduled';
	import { refreshScheduled } from '$lib/stores/scheduled';
	import { currentUser } from '$lib/stores/auth';

	let detail = $state<ScheduledDetail | null>(null);
	let busy = $state(false);
	let error = $state('');
	let confirmDelete = $state(false);
	let pollTimer: ReturnType<typeof setTimeout> | undefined;
	let destroyed = false;

	const id = $derived($page.params.id ?? '');
	const occurrenceInProgress = $derived(
		detail?.runs.some((run) => run.state === 'pending' || run.state === 'running') ?? false
	);

	onMount(() => {
		if ($currentUser?.scheduledEnabled === false) {
			void goto('/');
			return;
		}
		void load();
	});
	onDestroy(() => {
		destroyed = true;
		if (pollTimer) clearTimeout(pollTimer);
	});

	async function load(fromPoll = false): Promise<void> {
		try {
			const loaded = await getScheduledTask(id);
			if (destroyed) return;
			detail = loaded;
			error = '';
			schedulePoll();
			if (detail.runs.some((run) => run.unread)) {
				try {
					await markScheduledTaskRead(id);
					if (destroyed) return;
					detail = { ...detail, runs: detail.runs.map((run) => ({ ...run, unread: false })) };
					await refreshScheduled();
					if (destroyed) return;
				} catch (cause) {
					if (destroyed) return;
					error = cause instanceof Error ? cause.message : 'Could not mark results read';
				}
			}
		} catch (cause) {
			if (destroyed) return;
			error = cause instanceof Error ? cause.message : 'Could not load this task';
			if (fromPoll) schedulePoll();
		}
	}

	function schedulePoll(): void {
		if (pollTimer) clearTimeout(pollTimer);
		pollTimer = undefined;
		if (destroyed) return;
		if (!detail?.runs.some((run) => run.state === 'pending' || run.state === 'running')) return;
		pollTimer = setTimeout(() => {
			pollTimer = undefined;
			void load(true);
		}, 5000);
	}

	async function changeState(): Promise<void> {
		if (!detail || busy || (detail.task.state === 'active' && occurrenceInProgress)) return;
		busy = true;
		error = '';
		try {
			const task =
				detail.task.state === 'active'
					? await pauseScheduledTask(id)
					: await resumeScheduledTask(id);
			detail = { ...detail, task };
			await refreshScheduled();
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Could not update this task';
		} finally {
			busy = false;
		}
	}

	async function runNow(): Promise<void> {
		if (!detail || busy || occurrenceInProgress) return;
		busy = true;
		error = '';
		try {
			const run = await runScheduledTaskNow(id);
			detail = { ...detail, runs: [run, ...detail.runs] };
			schedulePoll();
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Could not start this task';
		} finally {
			busy = false;
		}
	}

	async function remove(): Promise<void> {
		confirmDelete = false;
		if (!detail || busy || occurrenceInProgress) return;
		busy = true;
		try {
			await deleteScheduledTask(id);
			await refreshScheduled();
			await goto('/scheduled');
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Could not delete this task';
			busy = false;
		}
	}

	function label(value: string): string {
		return value.replaceAll('_', ' ').replace(/^\w/, (letter) => letter.toUpperCase());
	}

	function formatTaskTime(value: string): string {
		if (!detail) return '';
		return new Intl.DateTimeFormat(undefined, {
			dateStyle: 'short',
			timeStyle: 'medium',
			timeZone: detail.task.timezone
		}).format(new Date(value));
	}
</script>

<svelte:head><title>{detail?.task.name ?? 'Scheduled task'} · Kadence</title></svelte:head>

{#if $currentUser?.scheduledEnabled !== false}
<section class="detail-page">
	<a class="back" href="/scheduled">← Scheduled</a>
	{#if error}<div class="error" role="alert">{error}</div>{/if}
	{#if !detail && !error}
		<p class="loading">Loading task…</p>
	{:else if detail}
		<header>
			<div>
				<span class={`state ${detail.task.state}`}>{label(detail.task.state)}</span>
				<h1>{detail.task.name}</h1>
			</div>
			<div class="controls">
				{#if detail.task.state === 'active' || detail.task.state === 'paused'}
					<button
						disabled={busy || (detail.task.state === 'active' && occurrenceInProgress)}
						onclick={changeState}
					>
						{detail.task.state === 'active' ? 'Pause' : 'Resume'}
					</button>
				{/if}
				<button
					disabled={busy || occurrenceInProgress || !['active', 'paused'].includes(detail.task.state)}
					onclick={runNow}
					>Run now</button
				>
				<button
					class="delete"
					disabled={busy || occurrenceInProgress}
					onclick={() => (confirmDelete = true)}>Delete task</button
				>
			</div>
		</header>
		{#if occurrenceInProgress}
			<p class="execution-note" role="status">
				Task controls are available when this run finishes.
			</p>
		{/if}

		<section class="overview" aria-label="Task overview">
			<div>
				<span>Next run</span>
				<strong>{detail.task.nextRunAt ? formatTaskTime(detail.task.nextRunAt) : 'Not scheduled'}</strong>
			</div>
			<div><span>Timezone</span><strong>{detail.task.timezone}</strong></div>
			<div>
				<span>Delivery</span>
				<strong>{detail.task.deliveryPolicy === 'on_change' ? 'When something changes' : 'Every run'}</strong>
			</div>
		</section>

		<section class="history">
			<h2>Run history</h2>
			{#if detail.runs.length === 0}
				<p class="empty">No runs yet. Use Run now or wait for the first scheduled time.</p>
			{:else}
				<ol>
					{#each detail.runs as run (run.id)}
						<li class:unread={run.unread}>
							<div class="run-head">
								<span class={`run-state ${run.state}`}>{label(run.state)}</span>
								<time datetime={run.scheduledFor}>{formatTaskTime(run.scheduledFor)}</time>
							</div>
							{#if run.result}<p>{run.result}</p>{/if}
							{#if run.error}<p class="run-error">{label(run.error)}</p>{/if}
							{#if run.state === 'no_change'}<p class="muted">Nothing changed.</p>{/if}
						</li>
					{/each}
				</ol>
			{/if}
		</section>
	{/if}
</section>
{/if}

<ConfirmDialog
	open={confirmDelete}
	title="Delete scheduled task"
	message="Delete this task? Its conversation and delivered results will remain available."
	confirmLabel="Delete"
	onConfirm={remove}
	onCancel={() => (confirmDelete = false)}
/>

<style>
	.detail-page { max-width: 880px; margin: 0 auto; padding: 34px 24px 60px; }
	.back { display: inline-block; margin-bottom: 24px; color: var(--text-muted); text-decoration: none; }
	header { display: flex; justify-content: space-between; align-items: flex-end; gap: 20px; }
	h1 { margin: 5px 0 0; font-size: 1.9rem; letter-spacing: -0.03em; }
	.state, .run-state { color: var(--accent); font: 600 0.72rem/1.4 ui-monospace, SFMono-Regular, Consolas, monospace; text-transform: uppercase; letter-spacing: 0.06em; }
	.state.paused, .state.failed, .run-state.failed { color: #b66a2c; }
	.controls { display: flex; gap: 7px; flex-wrap: wrap; justify-content: flex-end; }
	.execution-note { margin: 12px 0 0; color: var(--text-muted); font-size: 0.84rem; text-align: right; }
	button { padding: 8px 12px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); color: var(--text); font: inherit; cursor: pointer; }
	button.delete { color: var(--danger); }
	button:disabled { opacity: 0.5; cursor: not-allowed; }
	.overview { display: grid; grid-template-columns: repeat(3, 1fr); gap: 1px; margin: 30px 0 38px; border: 1px solid var(--border); border-radius: var(--radius); overflow: hidden; background: var(--border); }
	.overview div { display: grid; gap: 5px; padding: 16px; background: var(--surface); }
	.overview span { color: var(--text-muted); font-size: 0.8rem; }
	.overview strong { font: 600 0.84rem/1.45 ui-monospace, SFMono-Regular, Consolas, monospace; }
	.history h2 { font-size: 1rem; margin: 0 0 10px; }
	ol { list-style: none; margin: 0; padding: 0; border-top: 1px solid var(--border); }
	li { padding: 17px 4px; border-bottom: 1px solid var(--border); }
	li.unread { border-left: 3px solid var(--accent); padding-left: 12px; background: linear-gradient(90deg, #eef5f3, transparent 55%); }
	.run-head { display: flex; justify-content: space-between; gap: 16px; }
	time { color: var(--text-muted); font: 0.76rem/1.4 ui-monospace, SFMono-Regular, Consolas, monospace; }
	li p { margin: 10px 0 0; white-space: pre-wrap; }
	.run-error, .error { color: #8a4c18; }
	.error { margin-bottom: 16px; border-left: 3px solid #b66a2c; background: #fff8f1; padding: 9px 12px; }
	.muted, .empty, .loading { color: var(--text-muted); }
	button:focus-visible, a:focus-visible { outline: 3px solid color-mix(in srgb, var(--accent) 35%, transparent); outline-offset: 2px; }
	@media (max-width: 680px) {
		.detail-page { padding: 24px 16px 46px; }
		header { align-items: flex-start; flex-direction: column; }
		.controls { justify-content: flex-start; }
		.execution-note { text-align: left; }
		.overview { grid-template-columns: 1fr; }
		.run-head { flex-direction: column; gap: 3px; }
	}
</style>
