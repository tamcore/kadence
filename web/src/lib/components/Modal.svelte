<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		open = false,
		title,
		onClose,
		children
	}: {
		open?: boolean;
		title: string;
		onClose: () => void;
		children: Snippet;
	} = $props();

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') onClose();
	}
</script>

<svelte:window onkeydown={open ? onKeydown : undefined} />

{#if open}
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div class="backdrop" onclick={onClose}>
		<div class="card" role="dialog" tabindex="-1" aria-modal="true" aria-label={title} onclick={(e) => e.stopPropagation()}>
			<div class="head">
				<h2>{title}</h2>
				<button class="close" type="button" aria-label="Close" onclick={onClose}>&times;</button>
			</div>
			<div class="body">{@render children()}</div>
		</div>
	</div>
{/if}

<style>
	.backdrop {
		position: fixed;
		inset: 0;
		background: rgba(0, 0, 0, 0.4);
		display: flex;
		align-items: flex-start;
		justify-content: center;
		padding: 10vh 16px 16px;
		z-index: 100;
	}
	.card {
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: var(--radius);
		width: 100%;
		max-width: 420px;
		box-shadow: 0 12px 32px rgba(0, 0, 0, 0.18);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 16px 20px;
		border-bottom: 1px solid var(--border);
	}
	.head h2 {
		margin: 0;
		font-size: 1.1rem;
	}
	.close {
		background: none;
		border: none;
		font-size: 1.5rem;
		line-height: 1;
		cursor: pointer;
		color: var(--text-muted);
		padding: 0 4px;
	}
	.body {
		padding: 20px;
	}
</style>
