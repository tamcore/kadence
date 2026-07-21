<script lang="ts">
	import { onMount } from 'svelte';
	import {
		createMcp,
		deleteMcp,
		listMcp,
		updateMcp,
		type McpInput,
		type McpServer
	} from '$lib/api/mcp';
	import { isAdmin } from '$lib/stores/auth';
	import McpServerForm from '$lib/components/McpServerForm.svelte';
	import Button from '$lib/components/Button.svelte';

	let servers = $state<McpServer[]>([]);
	let canAdd = $state(false);
	let loading = $state(true);
	let error = $state<string | null>(null);

	let showAddForm = $state(false);
	let editingId = $state<number | null>(null);
	let formError = $state('');

	async function reload(): Promise<void> {
		const res = await listMcp();
		servers = res.servers;
		canAdd = res.canAdd;
	}

	onMount(async () => {
		try {
			await reload();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load MCP servers';
		} finally {
			loading = false;
		}
	});

	async function submitAdd(input: McpInput): Promise<void> {
		formError = '';
		try {
			await createMcp(input);
			showAddForm = false;
			await reload();
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Could not add MCP server';
		}
	}

	async function submitEdit(input: McpInput): Promise<void> {
		if (editingId === null) return;
		formError = '';
		try {
			await updateMcp(editingId, input);
			editingId = null;
			await reload();
		} catch (e) {
			formError = e instanceof Error ? e.message : 'Could not update MCP server';
		}
	}

	async function handleDelete(id: number): Promise<void> {
		try {
			await deleteMcp(id);
			await reload();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not delete MCP server';
		}
	}

	function startEdit(s: McpServer): void {
		formError = '';
		showAddForm = false;
		editingId = s.id ?? null;
	}

	function cancelEdit(): void {
		editingId = null;
		formError = '';
	}

	function startAdd(): void {
		formError = '';
		editingId = null;
		showAddForm = true;
	}

	function cancelAdd(): void {
		showAddForm = false;
		formError = '';
	}
</script>

<div class="page">
	<h1>MCP servers</h1>
	{#if loading}
		<p class="muted">Loading…</p>
	{:else if error}
		<div class="error" role="alert">{error}</div>
	{:else}
		{#if servers.length === 0}
			<p class="muted">No MCP servers available to you.</p>
		{:else}
			<ul class="list">
				{#each servers as s (`${s.scope}/${s.name}`)}
					<li>
						{#if editingId === s.id}
							<McpServerForm
								initial={{ name: s.name, url: s.url ?? '', transport: s.transport, authUser: '' }}
								submitLabel="Save"
								{formError}
								onSubmit={submitEdit}
								onCancel={cancelEdit}
							/>
						{:else}
							<div class="card">
								<a class="card-link" href="/mcp/{s.name}">
									<span class="dot {s.state}" title={s.state}></span>
									<span class="name">{s.name}</span>
									<span class="badge">{s.scope === 'global' ? 'Global' : 'Yours'}</span>
									<span class="muted">{s.transport} · {s.toolCount} tools</span>
									{#if $isAdmin && s.url}<span class="url muted">{s.url}</span>{/if}
								</a>
								{#if s.editable}
									<div class="row-actions">
										<Button variant="ghost" onclick={() => startEdit(s)}>Edit</Button>
										<Button variant="danger" onclick={() => handleDelete(s.id!)}>Delete</Button>
									</div>
								{/if}
							</div>
						{/if}
					</li>
				{/each}
			</ul>
		{/if}

		{#if canAdd}
			<div class="add-section">
				{#if showAddForm}
					<McpServerForm submitLabel="Add server" {formError} onSubmit={submitAdd} onCancel={cancelAdd} />
				{:else}
					<Button variant="primary" onclick={startAdd}>Add MCP server</Button>
				{/if}
			</div>
		{/if}
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
	}
	.card-link {
		display: flex;
		align-items: center;
		gap: 10px;
		flex: 1;
		text-decoration: none;
		color: inherit;
		min-width: 0;
	}
	.card:hover {
		background: var(--surface-hover, rgba(0, 0, 0, 0.03));
	}
	.dot {
		width: 10px;
		height: 10px;
		border-radius: 50%;
		background: var(--text-muted);
		flex-shrink: 0;
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
	.row-actions {
		display: flex;
		gap: 8px;
		flex-shrink: 0;
	}
	.add-section {
		margin-top: 16px;
	}
</style>
