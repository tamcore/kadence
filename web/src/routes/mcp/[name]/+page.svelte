<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/stores';
	import { getMcpTools, type McpTool } from '$lib/api/mcp';

	let name = $derived($page.params.name ?? '');
	let tools = $state<McpTool[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	onMount(async () => {
		try {
			const res = await getMcpTools(name);
			tools = res.tools;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Not found or not available to you';
		} finally {
			loading = false;
		}
	});
</script>

<div class="page">
	<p><a href="/mcp">&larr; MCP servers</a></p>
	<h1>{name}</h1>
	{#if loading}
		<p class="muted">Loading…</p>
	{:else if error}
		<div class="error" role="alert">{error}</div>
	{:else if tools.length === 0}
		<p class="muted">No tools.</p>
	{:else}
		{#each tools as t (t.name)}
			<div class="tool">
				<code class="tname">{t.name}</code>
				{#if t.description}<p class="muted">{t.description}</p>{/if}
				{#if t.inputSchema}
					<details>
						<summary class="muted">schema</summary>
						<pre>{JSON.stringify(t.inputSchema, null, 2)}</pre>
					</details>
				{/if}
			</div>
		{/each}
	{/if}
</div>

<style>
	.page {
		padding: 24px;
	}
	.muted {
		color: var(--text-muted);
	}
	.error {
		color: var(--danger);
	}
	.tool {
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 10px 12px;
		margin: 8px 0;
	}
	.tname {
		font-weight: 600;
	}
	pre {
		overflow-x: auto;
		background: var(--surface, rgba(0, 0, 0, 0.04));
		padding: 8px;
		border-radius: var(--radius);
	}
</style>
