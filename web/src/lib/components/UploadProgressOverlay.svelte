<script lang="ts">
	import { tick } from 'svelte';
	import Button from '$lib/components/Button.svelte';
	import { dismissUploadBatch, uploadBatch, type UploadLifecycle } from '$lib/stores/upload-progress';

	let returnFocus: HTMLElement | undefined;

	const isActive = $derived(
		$uploadBatch?.files.some((file) => file.state !== 'done' && file.state !== 'error') ?? false
	);
	const canDismiss = $derived(
		!isActive && ($uploadBatch?.files.some((file) => file.state === 'error') ?? false)
	);

	function stateLabel(state: UploadLifecycle): string {
		switch (state) {
			case 'queued':
				return 'Queued';
			case 'uploading':
				return 'Uploading…';
			case 'processing':
				return 'Processing…';
			case 'done':
				return 'Done';
			case 'error':
				return 'Failed';
		}
	}

	function focusHeading(node: HTMLElement): { destroy: () => void } {
		returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
		void tick().then(() => node.focus());
		return {
			destroy: () => {
				if (returnFocus?.isConnected) returnFocus.focus();
			}
		};
	}

	function dismiss(): void {
		if ($uploadBatch && canDismiss) dismissUploadBatch($uploadBatch.id);
	}

	function handleBackdropClick(event: MouseEvent): void {
		if (event.target !== event.currentTarget) return;
		if (!isActive) dismiss();
	}

	function handleKeydown(event: KeyboardEvent): void {
		if (event.key !== 'Escape') return;
		event.preventDefault();
		if (isActive) {
			event.stopImmediatePropagation();
			return;
		}
		dismiss();
	}
</script>

<svelte:window onkeydown={$uploadBatch ? handleKeydown : undefined} />

{#if $uploadBatch}
	<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
	<div class="backdrop" data-testid="upload-progress-backdrop" onclick={handleBackdropClick}>
		<div class="card" role="dialog" tabindex="-1" aria-modal="true" aria-labelledby="upload-progress-heading">
			<h2 id="upload-progress-heading" tabindex="-1" use:focusHeading>Uploading files</h2>
			<ul class="files" role="status" aria-live="polite" aria-atomic="true">
				{#each $uploadBatch.files as file (file.ordinal)}
					<li class:error={file.state === 'error'}>
						<span class="filename" title={file.filename}>{file.filename}</span>
						<span class="state">{stateLabel(file.state)}</span>
						{#if file.error}<span class="error-message">{file.error}</span>{/if}
					</li>
				{/each}
			</ul>
			{#if canDismiss}
				<div class="actions"><Button variant="ghost" onclick={dismiss}>Dismiss</Button></div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.backdrop {
		position: fixed;
		inset: 0;
		display: grid;
		place-items: center;
		padding: 16px;
		background: var(--overlay);
		z-index: 200;
	}
	.card {
		width: min(100%, 520px);
		max-height: min(100%, 640px);
		overflow: auto;
		padding: 20px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		box-shadow: 0 12px 32px color-mix(in srgb, var(--overlay) 45%, transparent);
	}
	h2 {
		margin: 0 0 16px;
		font-size: 1.1rem;
	}
	.files {
		display: grid;
		gap: 8px;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.files li {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 4px 16px;
		align-items: baseline;
		padding: 10px;
		border: 1px solid var(--border);
		border-radius: 6px;
	}
	.files li.error {
		border-color: color-mix(in srgb, var(--danger) 42%, var(--border));
	}
	.filename {
		overflow: hidden;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.state, .error-message {
		color: var(--text-muted);
	}
	.error-message {
		grid-column: 1 / -1;
	}
	.actions {
		display: flex;
		justify-content: flex-end;
		margin-top: 16px;
	}
</style>
