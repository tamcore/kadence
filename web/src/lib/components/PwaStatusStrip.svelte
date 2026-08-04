<script lang="ts">
	interface Props {
		online: boolean;
		updateAvailable: boolean;
		applyingUpdate: boolean;
		onReload: () => void;
	}

	let { online, updateAvailable, applyingUpdate, onReload }: Props = $props();
</script>

{#if !online}
	<div class="strip offline" role="status">
		<span class="signal" aria-hidden="true"></span>
		<span>You’re offline — server-backed features are unavailable.</span>
	</div>
{/if}

{#if updateAvailable}
	<div class="strip update" role="status">
		<span>Update available</span>
		<button type="button" onclick={onReload} disabled={applyingUpdate}>
			{applyingUpdate ? 'Reloading…' : 'Reload'}
		</button>
	</div>
{/if}

<style>
	.strip {
		min-height: 36px;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 9px;
		padding: 6px 12px;
		font-size: 0.85rem;
		font-weight: 550;
		text-align: center;
	}

	.offline {
		background: var(--warning-bg);
		border-bottom: 1px solid var(--warning);
		color: var(--warning-fg);
	}

	.signal {
		width: 8px;
		height: 8px;
		flex: 0 0 auto;
		border-radius: 50%;
		background: var(--warning);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--warning) 16%, transparent);
	}

	.update {
		background: var(--brand);
		color: var(--on-brand);
	}

	button {
		border: 1px solid color-mix(in srgb, var(--on-brand) 72%, transparent);
		border-radius: 5px;
		background: var(--on-brand);
		color: var(--brand);
		padding: 3px 10px;
		font: inherit;
		font-weight: 650;
		cursor: pointer;
	}

	button:hover:not(:disabled) {
		background: color-mix(in srgb, var(--brand) 6%, var(--on-brand));
	}

	button:focus-visible {
		outline: 3px solid color-mix(in srgb, var(--on-brand) 55%, transparent);
		outline-offset: 2px;
	}

	button:disabled {
		opacity: 0.65;
		cursor: wait;
	}
</style>
