<script lang="ts">
	import type { Document } from '$lib/types';
	import Button from '$lib/components/Button.svelte';

	let { documents, ondelete }: { documents: Document[]; ondelete: (id: number) => void } = $props();

	function fmt(ts: string): string {
		const d = new Date(ts);
		return isNaN(d.getTime()) ? ts : d.toLocaleString();
	}
</script>

{#if documents.length === 0}
	<p class="muted">No documents yet.</p>
{:else}
	<table>
		<thead><tr><th>Filename</th><th>Type</th><th>Scope</th><th>Added</th><th></th></tr></thead>
		<tbody>
			{#each documents as d (d.id)}
				<tr>
					<td>{d.filename}</td>
					<td>{d.source_type}</td>
					<td>{d.scope}</td>
					<td>{fmt(d.created_at)}</td>
					<td><Button variant="danger" onclick={() => ondelete(d.id)}>Delete</Button></td>
				</tr>
			{/each}
		</tbody>
	</table>
{/if}

<style>
	.muted { color: var(--text-muted); }
	table { width: 100%; border-collapse: collapse; }
	th, td { text-align: left; padding: 10px; border-bottom: 1px solid var(--border); }
</style>
