<script lang="ts">
	import { onMount } from 'svelte';
	import { listMcp, type McpServer } from '$lib/api/mcp';
	import { isAdmin } from '$lib/stores/auth';

	let servers = $state<McpServer[]>([]);
	let loading = $state(true);
	let error = $state<string | null>(null);

	onMount(async () => {
		try {
			servers = await listMcp();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load MCP servers';
		} finally {
			loading = false;
		}
	});
</script>

<div class="page">
	<h1>MCP servers</h1>
	{#if loading}
		<p class="muted">Loading…</p>
	{:else if error}
		<div class="error" role="alert">{error}</div>
	{:else if servers.length === 0}
		<p class="muted">No MCP servers available to you.</p>
	{:else}
		<ul class="list">
			{#each servers as s (s.name)}
				<li>
					<a class="card" href="/mcp/{s.name}">
						<span class="dot {s.state}" title={s.state}></span>
						<span class="name">{s.name}</span>
						<span class="badge">{s.scope === 'global' ? 'Global' : 'Yours'}</span>
						<span class="muted">{s.transport} · {s.toolCount} tools</span>
						{#if $isAdmin && s.url}<span class="url muted">{s.url}</span>{/if}
					</a>
				</li>
			{/each}
		</ul>
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
	.list {
		list-style: none;
		padding: 0;
		margin: 16px 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	.card {
		display: flex;
		align-items: center;
		gap: 10px;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		text-decoration: none;
		color: inherit;
	}
	.card:hover {
		background: var(--surface-hover, rgba(0, 0, 0, 0.03));
	}
	.dot {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--text-muted);
	}
	.dot.healthy {
		background: #1a9e5c;
	}
	.dot.unhealthy {
		background: var(--danger);
	}
	.dot.checking {
		background: var(--text-muted);
	}
	.name {
		font-weight: 600;
	}
	.badge {
		font-size: 0.7rem;
		border: 1px solid var(--border);
		border-radius: 999px;
		padding: 1px 8px;
	}
	.url {
		margin-left: auto;
		font-size: 0.75rem;
	}
</style>
