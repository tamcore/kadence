<script lang="ts">
	import { onMount } from 'svelte';
	import { getOverview, searchTerm, type KnowledgeOverview, type Snippet } from '$lib/api/context';

	let overview = $state<KnowledgeOverview | null>(null);
	let loading = $state(true);
	let error = $state<string | null>(null);
	let selected = $state<string | null>(null);
	let snippets = $state<Snippet[]>([]);

	async function load(): Promise<void> {
		loading = true;
		error = null;
		try {
			overview = await getOverview();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load your knowledge base';
		} finally {
			loading = false;
		}
	}

	function fontSize(weight: number): string {
		const weights = overview?.topTerms.map((t) => t.weight) ?? [weight];
		const min = Math.min(...weights);
		const max = Math.max(...weights);
		const scale = max === min ? 0.5 : (weight - min) / (max - min);
		return `${(0.9 + scale * 1.3).toFixed(2)}rem`;
	}

	async function pick(term: string): Promise<void> {
		selected = term;
		snippets = [];
		try {
			const result = await searchTerm(term);
			snippets = result.snippets;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load snippets';
		}
	}

	onMount(load);
</script>

<div class="page">
	<h1>Your knowledge</h1>
	{#if loading}
		<p class="muted">Loading…</p>
	{:else if error}
		<div class="error" role="alert">{error}</div>
	{:else if overview}
		{#if overview.topTerms.length === 0}
			<p class="muted">No knowledge yet — upload a document or chat to build it.</p>
		{:else}
			<p class="muted">
				{overview.documentCount} documents · {overview.conversationChunkCount} conversation memories
			</p>
			<div class="cloud">
				{#each overview.topTerms as t (t.term)}
					<button class="tag" style="font-size:{fontSize(t.weight)}" onclick={() => pick(t.term)}>
						{t.term}
					</button>
				{/each}
			</div>
			{#if selected}
				<h2 class="sel">&ldquo;{selected}&rdquo;</h2>
				{#if snippets.length === 0}
					<p class="muted">No snippets.</p>
				{/if}
				{#each snippets as s}
					<div class="snippet">
						<span class="src">{s.sourceKind}</span>
						<p>{s.content}</p>
					</div>
				{/each}
			{/if}
		{/if}
	{/if}
</div>

<style>
	.muted {
		color: var(--text-muted);
	}
	.cloud {
		display: flex;
		flex-wrap: wrap;
		gap: 8px 12px;
		align-items: baseline;
		margin: 16px 0;
	}
	.tag {
		background: none;
		border: none;
		padding: 2px;
		cursor: pointer;
		color: var(--accent);
		line-height: 1.2;
	}
	.tag:hover {
		text-decoration: underline;
	}
	.sel {
		margin-top: 20px;
	}
	.snippet {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px;
		margin: 8px 0;
	}
	.src {
		font-size: 0.75rem;
		color: var(--text-muted);
	}
	.snippet p {
		margin: 4px 0 0;
	}
	.error {
		color: var(--danger);
	}
</style>
