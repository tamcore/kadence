<script lang="ts">
	import type { Document } from '$lib/types';
	import Button from '$lib/components/Button.svelte';
	import ActionMenu from '$lib/components/ActionMenu.svelte';

	let { documents, ondelete }: { documents: Document[]; ondelete: (id: number) => void } = $props();

	function fmt(ts: string): string {
		const d = new Date(ts);
		return isNaN(d.getTime()) ? ts : d.toLocaleString();
	}
</script>

{#if documents.length === 0}
	<p class="muted">No documents yet.</p>
{:else}
	<table aria-label="Documents">
		<thead><tr><th>Filename</th><th>Type</th><th>Scope</th><th>Added</th><th></th></tr></thead>
		<tbody>
			{#each documents as d (d.id)}
				<tr aria-label={d.filename}>
					<td><span class="mobile-label">Filename</span><strong class="filename">{d.filename}</strong></td>
					<td><span class="mobile-label">Type</span><span>{d.source_type}</span></td>
					<td><span class="mobile-label">Scope</span><span>{d.scope}</span></td>
					<td><span class="mobile-label">Added</span><span>{fmt(d.created_at)}</span></td>
					<td class="actions-cell">
						<span class="desktop-action">
							<Button variant="danger" aria-label={`Delete ${d.filename}`} onclick={() => ondelete(d.id)}>Delete</Button>
						</span>
						<span class="mobile-actions">
							<ActionMenu
								label={`Actions for ${d.filename}`}
								items={[{ label: 'Delete', danger: true, onSelect: () => ondelete(d.id) }]}
							/>
						</span>
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
{/if}

<style>
	.muted { color: var(--text-muted); }
	table { width: 100%; border-collapse: collapse; }
	th, td { text-align: left; padding: 10px; border-bottom: 1px solid var(--border); }
	.filename { font-weight: 600; overflow-wrap: anywhere; }
	.mobile-label, .mobile-actions { display: none; }

	@media (max-width: 700px) {
		table, tbody { display: block; width: 100%; min-width: 0; }
		thead {
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
		tbody { display: grid; gap: 10px; }
		tr {
			position: relative;
			display: block;
			min-width: 0;
			padding: 12px 48px 12px 14px;
			border: 1px solid var(--border);
			border-radius: calc(var(--radius) + 2px);
			background: var(--surface);
		}
		td {
			display: grid;
			grid-template-columns: 72px minmax(0, 1fr);
			gap: 10px;
			min-width: 0;
			padding: 4px 0;
			border: 0;
			overflow-wrap: anywhere;
		}
		.mobile-label {
			display: block;
			color: var(--text-muted);
			font-size: 0.72rem;
			font-weight: 650;
			letter-spacing: 0.04em;
			text-transform: uppercase;
		}
		.actions-cell {
			position: absolute;
			top: 7px;
			right: 7px;
			display: block;
			width: auto;
			padding: 0;
		}
		.desktop-action { display: none; }
		.mobile-actions { display: block; }
	}
</style>
