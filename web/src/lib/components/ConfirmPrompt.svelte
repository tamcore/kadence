<script lang="ts">
	import { submitConfirmation } from '$lib/api/confirmations';
	import { confirmRequest } from '$lib/stores/chat';
	import { get } from 'svelte/store';
	import { canReachServer } from '$lib/stores/connection';
	import type { ConfirmRequest } from '$lib/types';
	import Button from '$lib/components/Button.svelte';

	let { request }: { request: ConfirmRequest } = $props();

	let submitting = $state(false);
	let error = $state('');

	// The tool call is blocked on this answer and the server gives up shortly,
	// so both buttons send an explicit decision. Silence would only ever be
	// read as a refusal, and later than necessary.
	async function answer(confirm: boolean): Promise<void> {
		if (submitting) return;
		submitting = true;
		error = '';
		try {
			await submitConfirmation(request.requestId, confirm);
			// Only clear what we answered. A second question can arrive while
			// this POST is in flight, and clearing blindly would erase a live
			// prompt the user has not seen yet.
			if (get(confirmRequest)?.requestId === request.requestId) {
				confirmRequest.set(null);
			}
		} catch {
			error = 'Could not send your answer. The action was not permitted.';
		} finally {
			submitting = false;
		}
	}
</script>

<section class="confirm-prompt">
	<p class="message">{request.message}</p>
	<p class="tool"><code>{request.tool}</code></p>
	{#if error}<div class="error" role="alert">{error}</div>{/if}
	<div class="actions">
		<Button
			type="button"
			variant="primary"
			disabled={submitting || !$canReachServer}
			onclick={() => answer(true)}
		>
			Allow
		</Button>
		<Button type="button" variant="ghost" disabled={submitting} onclick={() => answer(false)}>
			Decline
		</Button>
	</div>
</section>

<style>
	.confirm-prompt {
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 16px;
		border: 1px solid var(--warning, var(--border));
		border-radius: var(--radius);
		background: var(--surface);
	}
	.message {
		margin: 0;
		font-weight: 600;
	}
	.tool {
		margin: 0;
		font-size: 0.85rem;
		color: var(--text-muted);
	}
	.error {
		color: var(--danger);
		font-size: 0.9rem;
	}
	.actions {
		display: flex;
		gap: 8px;
	}
</style>
