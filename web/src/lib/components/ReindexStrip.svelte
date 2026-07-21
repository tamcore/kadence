<script lang="ts">
	interface Props {
		stale: number;
		total: number;
	}

	let { stale, total }: Props = $props();
	let done = $derived(total - stale);
</script>

{#if stale > 0}
	<div class="strip" role="status">
		Re-indexing your knowledge base — {done} of {total} done. Results may be temporarily incomplete.
	</div>
{/if}

<style>
	.strip {
		background: var(--accent);
		color: #fff;
		padding: 6px 12px;
		font-size: 0.85rem;
		text-align: center;
	}
</style>
