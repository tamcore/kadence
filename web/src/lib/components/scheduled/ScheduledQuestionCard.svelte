<script lang="ts">
	import type { ScheduledQuestion } from '$lib/api/scheduled';
	import { canReachServer } from '$lib/stores/connection';

	let {
		question,
		onAnswer,
		onBack,
		onClose,
		initialAnswer = '',
		disabled = false,
		autofocus = true
	}: {
		question: ScheduledQuestion;
		onAnswer: (answer: string) => void;
		onBack?: () => void;
		onClose?: () => void;
		initialAnswer?: string;
		disabled?: boolean;
		autofocus?: boolean;
	} = $props();
	const instanceID = $props.id();
	const headingID = `question-${instanceID}`;
	const customInputID = `question-custom-${instanceID}`;
	const textInputID = `question-text-${instanceID}`;
	const optionInputID = (value: string): string => `question-option-${instanceID}-${value}`;

	function initialValues(): string[] {
		return initialAnswer
			.split(',')
			.map((value) => value.trim())
			.filter(Boolean);
	}

	function initialText(): string {
		return initialAnswer;
	}

	function optionValues(): Set<string> {
		return new Set((question.options ?? []).map((option) => option.value));
	}

	function initialSelected(): string[] {
		const options = optionValues();
		return initialValues().filter((value) => options.has(value));
	}

	function initialCustom(): string {
		const options = optionValues();
		return initialValues()
			.filter((value) => !options.has(value))
			.join(', ');
	}

	let selected = $state(initialSelected());
	let custom = $state(initialCustom());
	let text = $state(initialText());
	function focusCard(node: Element): void {
		(node as HTMLElement).focus();
	}

	function toggle(value: string): void {
		selected = selected.includes(value)
			? selected.filter((item) => item !== value)
			: [...selected, value];
	}

	function submitMulti(): void {
		const values = [...selected];
		if (custom.trim()) values.push(custom.trim());
		if (values.length) onAnswer(values.join(', '));
	}

	function submitText(): void {
		if (text.trim()) onAnswer(text.trim());
	}
</script>

<section
	class="question"
	aria-labelledby={headingID}
	tabindex="-1"
	{@attach autofocus ? focusCard : undefined}
>
	<header>
		<div class="question-nav">
			<button aria-label="Back" disabled={disabled} onclick={() => onBack?.()}>←</button>
			<span class="step" aria-hidden="true">Clarify</span>
			<button aria-label="Close question" disabled={disabled} onclick={() => onClose?.()}>×</button>
		</div>
		<h2 id={headingID}>{question.prompt}</h2>
		{#if question.kind === 'multi_select'}<p>Select all that apply.</p>{/if}
	</header>

	{#if question.kind === 'single_select'}
		<div class="choices">
			{#each question.options ?? [] as option (option.value)}
				<button
					aria-pressed={selected.includes(option.value)}
					disabled={disabled}
					onclick={() => onAnswer(option.value)}>{option.label}</button
				>
			{/each}
		</div>
		{#if question.allowCustom}
			<form
				onsubmit={(event) => {
					event.preventDefault();
					if (custom.trim()) onAnswer(custom.trim());
				}}
			>
				<label for={customInputID}>
					<span>Something else</span>
					<input id={customInputID} bind:value={custom} disabled={disabled} />
				</label>
				<button class="continue" type="submit" disabled={disabled || !custom.trim() || !$canReachServer}>Continue</button>
			</form>
		{/if}
		{#if question.optional}
			<div class="actions">
				<button class="skip" disabled={disabled} onclick={() => onAnswer('Skip')}>Skip</button>
			</div>
		{/if}
	{:else if question.kind === 'multi_select'}
		<div class="checks">
			{#each question.options ?? [] as option (option.value)}
				<label for={optionInputID(option.value)}>
					<input
						id={optionInputID(option.value)}
						type="checkbox"
						checked={selected.includes(option.value)}
						disabled={disabled}
						onchange={() => toggle(option.value)}
					/>
					<span>{option.label}</span>
				</label>
			{/each}
			{#if question.allowCustom}
				<label class="custom" for={customInputID}>
					<span>Something else</span>
					<input id={customInputID} bind:value={custom} disabled={disabled} />
				</label>
			{/if}
		</div>
		<div class="actions">
			{#if question.optional}
				<button class="skip" disabled={disabled} onclick={() => onAnswer('Skip')}>Skip</button>
			{/if}
			<button
				class="continue"
				disabled={disabled || (selected.length === 0 && !custom.trim())}
				onclick={submitMulti}>Continue</button
			>
		</div>
	{:else}
		<form
			onsubmit={(event) => {
				event.preventDefault();
				submitText();
			}}
		>
			<label for={textInputID}>
				<span>Your answer</span>
				<input id={textInputID} bind:value={text} disabled={disabled} />
			</label>
			<div class="actions">
				{#if question.optional}
					<button type="button" class="skip" disabled={disabled} onclick={() => onAnswer('Skip')}
						>Skip</button
					>
				{/if}
				<button class="continue" type="submit" disabled={disabled || !text.trim() || !$canReachServer}>Continue</button>
			</div>
		</form>
	{/if}
</section>

<style>
	.question {
		border: 1px solid var(--border);
		border-radius: calc(var(--radius) + 4px);
		background: var(--surface);
		box-shadow: var(--shadow);
		padding: 22px;
	}
	header { margin-bottom: 18px; }
	.question-nav { display: flex; align-items: center; justify-content: space-between; gap: 10px; }
	.question-nav button { border: 0; background: transparent; color: var(--text-muted); font: inherit; line-height: 1; cursor: pointer; padding: 3px 6px; }
	h2 { margin: 4px 0; font-size: 1.08rem; }
	p { margin: 0; color: var(--text-muted); font-size: 0.9rem; }
	.step {
		color: var(--accent);
		font: 600 0.7rem/1 ui-monospace, SFMono-Regular, Consolas, monospace;
		letter-spacing: 0.08em;
		text-transform: uppercase;
	}
	.choices, .checks { display: grid; gap: 8px; }
	.choices button, .checks label {
		width: 100%;
		padding: 11px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--text);
		text-align: left;
		font: inherit;
	}
	.choices button { cursor: pointer; }
	.choices button:hover:not(:disabled) { border-color: var(--accent); background: var(--accent-tint); }
	.checks label { display: flex; align-items: center; gap: 10px; }
	.checks .custom { display: grid; align-items: stretch; }
	form, form label { display: grid; gap: 8px; }
	form { margin-top: 10px; }
	input:not([type='checkbox']) {
		width: 100%;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--text);
		font: inherit;
	}
	.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 14px; }
	.continue, .skip {
		padding: 9px 15px;
		border-radius: var(--radius);
		font: inherit;
		font-weight: 600;
		cursor: pointer;
	}
	.continue { border: 1px solid var(--accent); background: var(--accent); color: var(--on-accent); }
	.skip { border: 1px solid var(--border); background: transparent; color: var(--text); }
	button:disabled { opacity: 0.55; cursor: not-allowed; }
	button:focus-visible, input:focus-visible { outline: 3px solid color-mix(in srgb, var(--accent) 35%, transparent); outline-offset: 2px; }
	@media (max-width: 520px) { .question { padding: 17px; } }
</style>
