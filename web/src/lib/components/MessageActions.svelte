<script lang="ts">
	import { onDestroy } from 'svelte';

	let {
		content,
		disabled = false,
		onEdit,
		onRegenerate
	}: {
		content: string;
		disabled?: boolean;
		onEdit?: () => void;
		onRegenerate?: () => void;
	} = $props();

	let copied = $state(false);
	let status = $state('');
	let resetTimer: ReturnType<typeof setTimeout> | undefined;

	async function copyMessage(): Promise<void> {
		try {
			await navigator.clipboard.writeText(content);
			copied = true;
			status = 'Copied';
			if (resetTimer) clearTimeout(resetTimer);
			resetTimer = setTimeout(() => {
				copied = false;
				status = '';
			}, 2000);
		} catch {
			status = 'Could not copy';
		}
	}

	onDestroy(() => {
		if (resetTimer) clearTimeout(resetTimer);
	});
</script>

<div class="message-actions">
	<button
		class="message-action"
		type="button"
		aria-label="Copy message"
		title={copied ? 'Copied' : 'Copy message'}
		{disabled}
		onclick={copyMessage}
	>
		{#if copied}
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="m5 12 4 4L19 6" />
			</svg>
		{:else}
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<rect x="8" y="8" width="11" height="11" rx="2" />
				<path d="M16 8V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v9a2 2 0 0 0 2 2h3" />
			</svg>
		{/if}
	</button>
	{#if onEdit}
		<button
			class="message-action"
			type="button"
			aria-label="Edit message"
			title="Edit message"
			{disabled}
			onclick={onEdit}
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="m4 20 4.5-1 10-10a2.1 2.1 0 0 0-3-3l-10 10L4 20Z" />
				<path d="m14 7 3 3" />
			</svg>
		</button>
	{/if}
	{#if onRegenerate}
		<button
			class="message-action"
			type="button"
			aria-label="Regenerate response"
			title="Regenerate response"
			{disabled}
			onclick={onRegenerate}
		>
			<svg viewBox="0 0 24 24" aria-hidden="true">
				<path d="M20 7v5h-5" />
				<path d="M19 12a7 7 0 1 1-2-5" />
			</svg>
		</button>
	{/if}
	<span class="status" aria-live="polite">{status}</span>
</div>

<style>
	.message-actions {
		display: flex;
		align-items: center;
		gap: 2px;
		min-height: 32px;
	}
	.message-action {
		width: 32px;
		height: 32px;
		display: inline-flex;
		align-items: center;
		justify-content: center;
		padding: 0;
		border: 0;
		border-radius: 8px;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
	}
	.message-action:hover:not(:disabled) {
		background: var(--bg);
		color: var(--text);
	}
	.message-action:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}
	.message-action:disabled {
		opacity: 0.45;
		cursor: not-allowed;
	}
	svg {
		width: 19px;
		height: 19px;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.status {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
		border: 0;
	}
	@media (pointer: coarse) {
		.message-action {
			width: 44px;
			height: 44px;
		}
		.message-actions {
			min-height: 44px;
		}
	}
</style>
