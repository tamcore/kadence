<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { onDestroy, onMount } from 'svelte';
	import Composer from '$lib/components/Composer.svelte';
	import ScheduledProposal from '$lib/components/scheduled/ScheduledProposal.svelte';
	import ScheduledQuestionCard from '$lib/components/scheduled/ScheduledQuestionCard.svelte';
	import {
		confirmScheduledTask,
		getScheduledTask,
		streamScheduledDefinition,
		type ScheduledProposal as Proposal,
		type ScheduledQuestion
	} from '$lib/api/scheduled';
	import { APIError } from '$lib/api/client';
	import { currentUser } from '$lib/stores/auth';
	import {
		loadMoreScheduled,
		refreshScheduled,
		scheduledHasMore,
		scheduledLoadingMore,
		scheduledRefreshError,
		scheduledTasks
	} from '$lib/stores/scheduled';

	let taskId = $state<string | null>(null);
	let turns = $state<{ role: 'user' | 'assistant'; content: string }[]>([]);
	let questionHistory = $state<{ question: ScheduledQuestion; answer?: string }[]>([]);
	let questionIndex = $state(-1);
	let coachText = $state('');
	let question = $state<ScheduledQuestion | null>(null);
	let proposal = $state<Proposal | null>(null);
	let sending = $state(false);
	let error = $state('');
	let controller: AbortController | null = null;

	const defining = $derived(taskId !== null || sending || turns.length > 0);
	const featureAvailable = $derived(!$currentUser || $currentUser.scheduledEnabled);

	function draftStorageKey(id: string): string {
		return `kadence_scheduled_draft:${$currentUser?.id ?? 'anonymous'}:${id}`;
	}

	function persistDraftUI(id: string): void {
		if (typeof sessionStorage === 'undefined') return;
		try {
			sessionStorage.setItem(
				draftStorageKey(id),
				JSON.stringify({ question, proposal, questionHistory, questionIndex })
			);
		} catch {
			// Draft persistence is a convenience; storage may be unavailable or full.
		}
	}

	function restoreDraftUI(id: string): void {
		if (typeof sessionStorage === 'undefined') return;
		try {
			const value = sessionStorage.getItem(draftStorageKey(id));
			if (!value) return;
			const stored = JSON.parse(value) as {
				question?: ScheduledQuestion | null;
				proposal?: Proposal | null;
				questionHistory?: { question: ScheduledQuestion; answer?: string }[];
				questionIndex?: number;
			};
			question = stored.question ?? null;
			proposal = stored.proposal ?? null;
			questionHistory = stored.questionHistory ?? (question ? [{ question }] : []);
			questionIndex =
				stored.questionIndex ?? (questionHistory.length > 0 ? questionHistory.length - 1 : -1);
		} catch {
			sessionStorage.removeItem(draftStorageKey(id));
		}
	}

	function clearDraftUI(id: string): void {
		if (typeof sessionStorage === 'undefined') return;
		try {
			sessionStorage.removeItem(draftStorageKey(id));
		} catch {
			// Ignore unavailable browser storage after a successful confirmation.
		}
	}

	function formatTaskTime(value: string, timezone: string): string {
		return new Intl.DateTimeFormat(undefined, {
			dateStyle: 'short',
			timeStyle: 'medium',
			timeZone: timezone
		}).format(new Date(value));
	}

	onMount(() => {
		if ($currentUser && !$currentUser.scheduledEnabled) {
			void goto('/');
			return;
		}
		void refreshScheduled();
		const resumeID = $page.url.searchParams.get('task');
		if (resumeID) void resumeDraft(resumeID);
	});
	onDestroy(() => controller?.abort());

	async function send(message: string): Promise<void> {
		if (sending) return;
		turns = [...turns, { role: 'user', content: message }];
		sending = true;
		error = '';
		question = null;
		proposal = null;
		if (taskId) persistDraftUI(taskId);
		coachText = '';
		controller?.abort();
		controller = new AbortController();
		try {
			for await (const event of streamScheduledDefinition(
				{ taskId: taskId ?? undefined, message },
				controller.signal
			)) {
				switch (event.type) {
					case 'meta':
						taskId = event.taskId;
						persistDraftUI(event.taskId);
						void goto(`/scheduled?task=${encodeURIComponent(event.taskId)}`, {
							replaceState: true,
							keepFocus: true
						});
						break;
					case 'text':
						coachText += event.delta;
						break;
					case 'task_question':
						recordQuestion(event.question);
						if (taskId) persistDraftUI(taskId);
						break;
					case 'task_proposal':
						proposal = event.proposal;
						if (taskId) persistDraftUI(taskId);
						break;
					case 'error':
						error = event.error;
						break;
				}
			}
			if (coachText) {
				turns = [...turns, { role: 'assistant', content: coachText }];
				coachText = '';
			}
		} catch (cause) {
			if (!(cause instanceof DOMException && cause.name === 'AbortError')) {
				error = cause instanceof Error ? cause.message : 'Could not refine this task';
			}
		} finally {
			sending = false;
		}
	}

	async function resumeDraft(resumeID: string): Promise<void> {
		sending = true;
		error = '';
		try {
			const loaded = await getScheduledTask(resumeID);
			if (loaded.task.state !== 'draft') {
				await goto(`/scheduled/${resumeID}`);
				return;
			}
			taskId = resumeID;
			turns = loaded.definitionMessages.map((message) => ({
				role: message.role,
				content: message.text
			}));
			questionHistory = definitionQuestionHistory(loaded.definitionMessages);
			questionIndex = questionHistory.length - 1;
			const latest = loaded.definitionMessages.at(-1);
			question = latest?.role === 'assistant' ? (latest.question ?? null) : null;
			if (loaded.task.compiledPrompt) {
				proposal = {
					version: loaded.task.version,
					name: loaded.task.name,
					taskKind: loaded.task.kind,
					compiledPrompt: loaded.task.compiledPrompt,
					executionMode: loaded.task.executionMode,
					schedule: {
						At: loaded.task.oneOffAt,
						DTStart: loaded.task.dtStart,
						RRULE: loaded.task.rrule,
						Timezone: loaded.task.timezone
					},
					timezone: loaded.task.timezone,
					authorizedTools: loaded.task.authorizedTools ?? [],
					deliveryPolicy: loaded.task.deliveryPolicy,
					initialRun: loaded.task.initialRun,
					stopCondition: loaded.task.stopCondition,
					staticMessage: loaded.task.staticMessage
				};
				persistDraftUI(resumeID);
			} else if (!question) {
				restoreDraftUI(resumeID);
			}
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Could not resume this task';
			taskId = resumeID;
		} finally {
			sending = false;
		}
	}

	async function confirm(expectedVersion: number): Promise<void> {
		if (!taskId || sending) return;
		sending = true;
		error = '';
		try {
			await confirmScheduledTask(taskId, expectedVersion);
			clearDraftUI(taskId);
			await refreshScheduled();
			await goto(`/scheduled/${taskId}`);
		} catch (cause) {
			error =
				cause instanceof APIError && cause.status === 409
					? 'This plan changed while you were reviewing it. Refine it again to see the latest version.'
					: cause instanceof Error
						? cause.message
						: 'Could not schedule this task';
		} finally {
			sending = false;
		}
	}

	function resetDefinition(): void {
		controller?.abort();
		taskId = null;
		turns = [];
		questionHistory = [];
		questionIndex = -1;
		coachText = '';
		question = null;
		proposal = null;
		error = '';
		void goto('/scheduled', { replaceState: true });
	}

	function definitionQuestionHistory(
		messages: {
			role: 'user' | 'assistant';
			text: string;
			question?: ScheduledQuestion;
		}[]
	): { question: ScheduledQuestion; answer?: string }[] {
		const history: { question: ScheduledQuestion; answer?: string }[] = [];
		let unanswered = -1;
		for (const message of messages) {
			if (message.role === 'assistant' && message.question) {
				history.push({ question: message.question });
				unanswered = history.length - 1;
			} else if (message.role === 'user' && unanswered >= 0) {
				history[unanswered] = { ...history[unanswered], answer: message.text };
				unanswered = -1;
			}
		}
		return history;
	}

	function recordQuestion(nextQuestion: ScheduledQuestion): void {
		const current = questionHistory[questionIndex];
		if (current && current.question.id === nextQuestion.id && current.answer === undefined) {
			questionHistory[questionIndex] = { ...current, question: nextQuestion };
			questionHistory = [...questionHistory];
		} else {
			questionHistory = [...questionHistory, { question: nextQuestion }];
			questionIndex = questionHistory.length - 1;
		}
		question = nextQuestion;
	}

	function answerQuestion(value: string): void {
		if (questionIndex >= 0) {
			questionHistory = questionHistory
				.slice(0, questionIndex + 1)
				.map((entry, index) => (index === questionIndex ? { ...entry, answer: value } : entry));
		}
		void send(value);
	}

	function showPreviousQuestion(): void {
		if (questionIndex <= 0) return;
		questionIndex -= 1;
		question = questionHistory[questionIndex].question;
		proposal = null;
		if (taskId) persistDraftUI(taskId);
	}

	function statusLabel(value: string): string {
		return value.charAt(0).toUpperCase() + value.slice(1).replace('_', ' ');
	}

	function runInProgress(state?: string): boolean {
		return state === 'pending' || state === 'running';
	}
</script>

<svelte:head><title>Scheduled · Kadence</title></svelte:head>

{#if featureAvailable}
	<section class:definition={defining} class="scheduled-page">
		<header class="page-head">
			<div>
				<p class="kicker">Your coach, on cadence</p>
				<h1>Scheduled</h1>
			</div>
			{#if defining}<button class="new-task" onclick={resetDefinition}>New task</button>{/if}
		</header>

		{#if !defining}
			<div class="landing">
				<p class="intro">Ask Kadence to check, remind, or analyze something later.</p>
				<div class="composer-wrap">
					<Composer
						disabled={sending}
						onSubmit={(text) => void send(text)}
						placeholder="Describe what should happen later…"
					/>
				</div>
				<div class="examples" aria-label="Task examples">
					<button onclick={() => void send('Give me feedback after my next run')}>
						<strong>Post-run feedback</strong>
						<span>Give me feedback after my next run</span>
					</button>
					<button onclick={() => void send('Remind me to plan my training every Sunday')}>
						<strong>Training reminder</strong>
						<span>Plan the week every Sunday</span>
					</button>
					<button onclick={() => void send('Watch my recovery trend and tell me when it changes')}>
						<strong>Recovery watch</strong>
						<span>Tell me when my trend changes</span>
					</button>
				</div>
			</div>
		{:else}
			<div class="definition-thread">
				<div class="rail" aria-hidden="true">
					<span class="node complete"></span>
					<span class:complete={coachText !== ''} class="node"></span>
					<span class:complete={question !== null || proposal !== null} class="node"></span>
				</div>
				<div class="thread">
					{#each turns as turn, index (`${index}-${turn.role}`)}
						{#if turn.role === 'user'}
							<div class="bubble user">{turn.content}</div>
						{:else}
							<p class="coach">{turn.content}</p>
						{/if}
					{/each}
					{#if coachText}<p class="coach" aria-live="polite">{coachText}</p>{/if}
					{#if error}<div class="error" role="alert">{error}</div>{/if}
					{#if question}
						{#key question.id}
							<ScheduledQuestionCard
								{question}
								initialAnswer={questionHistory[questionIndex]?.answer ?? ''}
								disabled={sending}
								onAnswer={answerQuestion}
								onBack={showPreviousQuestion}
								onClose={resetDefinition}
							/>
						{/key}
					{:else if proposal}
						<ScheduledProposal {proposal} disabled={sending} onConfirm={(version) => void confirm(version)} />
					{:else if sending}
						<p class="thinking" aria-live="polite">Thinking through the details…</p>
					{/if}
					{#if taskId && !question && !proposal && !sending}
						<div class="composer-wrap">
							<Composer
								onSubmit={(text) => void send(text)}
								placeholder="Continue refining this task…"
							/>
						</div>
					{/if}
				</div>
			</div>
		{/if}

		{#if !defining && $scheduledTasks.length > 0}
			<section class="task-section">
				<div class="section-head"><h2>Your tasks</h2><span>{$scheduledTasks.length}</span></div>
				{#if $scheduledRefreshError}<p class="refresh-error">Couldn’t refresh tasks.</p>{/if}
				<ul class="task-list">
					{#each $scheduledTasks as task (task.id)}
						<li>
							<a href={task.state === 'draft' ? `/scheduled?task=${task.id}` : `/scheduled/${task.id}`}>
								<span class="task-title">
									<span class="task-name">{task.name || 'Untitled task'}</span>
									{#if task.unreadCount}
										<span class="task-unread" aria-label={`${task.unreadCount} unread results`}
											>{task.unreadCount}</span
										>
									{/if}
								</span>
								<span class={`status ${task.state}`}>{statusLabel(task.state)}</span>
								<span class="next">
									{task.nextRunAt
										? `Next ${formatTaskTime(task.nextRunAt, task.timezone)} · ${task.timezone}`
										: 'No next run'}
								</span>
								{#if task.recentRun}
									<span class:in-progress={runInProgress(task.recentRun.state)} class="recent">
										Latest: {statusLabel(task.recentRun.state)}
										{#if runInProgress(task.recentRun.state)}
											· Task controls unavailable until this run finishes
										{/if}
									</span>
								{/if}
							</a>
						</li>
					{/each}
				</ul>
				{#if $scheduledHasMore}
					<div class="task-list-actions">
						<button
							type="button"
							aria-label={$scheduledLoadingMore ? 'Loading more tasks' : 'Load more tasks'}
							aria-busy={$scheduledLoadingMore}
							disabled={$scheduledLoadingMore}
							onclick={() => void loadMoreScheduled()}
						>
							{$scheduledLoadingMore ? 'Loading more…' : 'Load more'}
						</button>
					</div>
				{/if}
			</section>
		{/if}
	</section>
{/if}

<style>
	.scheduled-page { max-width: 820px; margin: 0 auto; min-height: 100%; padding: 34px 24px 56px; }
	.page-head { display: flex; align-items: flex-end; justify-content: space-between; gap: 20px; }
	h1 { margin: 0; font-size: 2rem; letter-spacing: -0.035em; }
	.kicker { margin: 0 0 3px; color: var(--accent); font: 600 0.72rem/1.4 ui-monospace, SFMono-Regular, Consolas, monospace; text-transform: uppercase; letter-spacing: 0.08em; }
	.new-task { border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); padding: 8px 12px; font: inherit; cursor: pointer; }
	.landing { min-height: 46vh; display: flex; flex-direction: column; justify-content: center; gap: 20px; }
	.intro { text-align: center; color: var(--text-muted); font-size: 1.08rem; margin: 0; }
	.composer-wrap { width: 100%; }
	.examples { display: grid; grid-template-columns: repeat(3, 1fr); gap: 8px; }
	.examples button { display: grid; gap: 3px; text-align: left; padding: 12px; border: 1px solid var(--border); border-radius: var(--radius); background: transparent; color: var(--text); font: inherit; cursor: pointer; }
	.examples button:hover { background: #eef5f3; border-color: color-mix(in srgb, var(--accent) 45%, var(--border)); }
	.examples span { color: var(--text-muted); font-size: 0.82rem; }
	.definition-thread { position: relative; display: grid; grid-template-columns: 24px minmax(0, 1fr); gap: 18px; margin: 52px auto 0; max-width: 720px; }
	.rail { position: relative; display: flex; flex-direction: column; justify-content: space-between; align-items: center; min-height: 310px; padding: 8px 0 22px; }
	.rail::before { content: ''; position: absolute; top: 13px; bottom: 27px; width: 1px; background: color-mix(in srgb, var(--accent) 35%, var(--border)); }
	.node { position: relative; width: 9px; height: 9px; border: 2px solid var(--border); border-radius: 50%; background: var(--surface); }
	.node.complete { border-color: var(--accent); background: var(--accent); box-shadow: 0 0 0 4px #eef5f3; }
	.thread { min-width: 0; display: flex; flex-direction: column; gap: 24px; }
	.bubble { align-self: flex-end; max-width: 82%; padding: 11px 14px; border-radius: 16px 16px 4px 16px; background: #eef5f3; }
	.answer { color: var(--text-muted); font-size: 0.92rem; }
	.coach { margin: 0; font-size: 1.04rem; }
	.thinking { color: var(--text-muted); }
	.error { border-left: 3px solid #b66a2c; padding: 9px 12px; background: #fff8f1; color: #754019; border-radius: 0 var(--radius) var(--radius) 0; }
	.task-section { border-top: 1px solid var(--border); padding-top: 22px; }
	.section-head { display: flex; align-items: baseline; gap: 8px; }
	.section-head h2 { font-size: 1rem; margin: 0; }
	.section-head span { color: var(--text-muted); font: 0.75rem ui-monospace, SFMono-Regular, Consolas, monospace; }
	.task-list { list-style: none; padding: 0; margin: 10px 0 0; display: grid; }
	.task-list li { border-top: 1px solid var(--border); }
	.task-list a { display: grid; grid-template-columns: 1fr auto; gap: 4px 12px; padding: 14px 4px; text-decoration: none; color: var(--text); }
	.task-name { font-weight: 600; }
	.task-title { display: flex; align-items: center; min-width: 0; gap: 7px; }
	.task-unread { min-width: 1.25rem; height: 1.25rem; display: inline-grid; place-items: center; border-radius: 999px; background: var(--accent); color: #fff; font: 600 0.68rem/1 ui-monospace, SFMono-Regular, Consolas, monospace; }
	.status { align-self: start; padding: 2px 7px; border-radius: 999px; background: #eef5f3; color: var(--accent); font: 0.7rem/1.4 ui-monospace, SFMono-Regular, Consolas, monospace; }
	.status.paused, .status.failed { background: #fff3e8; color: #b66a2c; }
	.next { grid-column: 1 / -1; color: var(--text-muted); font: 0.78rem/1.4 ui-monospace, SFMono-Regular, Consolas, monospace; }
	.recent { grid-column: 1 / -1; color: var(--text-muted); font-size: 0.78rem; }
	.refresh-error { color: #b66a2c; }
	.task-list-actions { display: flex; justify-content: center; padding-top: 14px; }
	.task-list-actions button {
		padding: 8px 16px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--text);
		font: inherit;
		font-weight: 600;
		cursor: pointer;
	}
	.task-list-actions button:disabled { cursor: wait; opacity: 0.65; }
	.task-list-actions button:focus-visible { outline: 3px solid color-mix(in srgb, var(--accent) 35%, transparent); outline-offset: 2px; }
	@media (max-width: 640px) {
		.scheduled-page { padding: 24px 16px 42px; }
		.examples { grid-template-columns: 1fr; }
		.definition-thread { margin-top: 34px; grid-template-columns: 18px minmax(0, 1fr); gap: 10px; }
		.bubble { max-width: 94%; }
	}
	@media (prefers-reduced-motion: reduce) { * { scroll-behavior: auto !important; } }
</style>
