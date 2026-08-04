<script lang="ts">
	interface Props {
		unhealthy: number;
		total: number;
	}

	let { unhealthy, total }: Props = $props();
</script>

{#if unhealthy > 0}
	<div class="strip" role="status">
		<a href="/mcp">{unhealthy} of {total} MCP servers unavailable</a>
	</div>
{/if}

<style>
	.strip {
		background: var(--danger);
		color: var(--on-accent);
		padding: 6px 12px;
		font-size: 0.85rem;
		text-align: center;
	}
	.strip a {
		color: var(--on-accent);
		text-decoration: underline;
	}
</style>
