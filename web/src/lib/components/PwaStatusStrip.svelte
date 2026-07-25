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
		background: #fff4d6;
		border-bottom: 1px solid #e4c36a;
		color: #5f3d00;
	}

	.signal {
		width: 8px;
		height: 8px;
		flex: 0 0 auto;
		border-radius: 50%;
		background: #c17b00;
		box-shadow: 0 0 0 3px rgba(193, 123, 0, 0.16);
	}

	.update {
		background: #021c46;
		color: #fff;
	}

	button {
		border: 1px solid rgba(255, 255, 255, 0.72);
		border-radius: 5px;
		background: #fff;
		color: #021c46;
		padding: 3px 10px;
		font: inherit;
		font-weight: 650;
		cursor: pointer;
	}

	button:hover:not(:disabled) {
		background: #edf3fb;
	}

	button:focus-visible {
		outline: 3px solid rgba(255, 255, 255, 0.55);
		outline-offset: 2px;
	}

	button:disabled {
		opacity: 0.65;
		cursor: wait;
	}
</style>
