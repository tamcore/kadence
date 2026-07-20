<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api } from '$lib/api/client';
	import { conversations, newChat, refreshConversations, removeConversation } from '$lib/stores/chat';
	import { clearAuth, currentUser, isAdmin } from '$lib/stores/auth';
	import { closeSidebar } from '$lib/stores/ui';
	import { onMount } from 'svelte';

	// The sidebar is global, so load history on every shell route (incl. home)
	// — the start page greets with the conversation history.
	onMount(refreshConversations);

	function startNew(): void {
		newChat();
		goto('/');
		closeSidebar();
	}

	async function del(id: number, e: Event): Promise<void> {
		e.preventDefault();
		e.stopPropagation();
		const wasActive = String(id) === $page.params.id;
		await removeConversation(id);
		if (wasActive) {
			goto('/');
			closeSidebar();
		}
	}

	async function handleLogout(): Promise<void> {
		try {
			await api.logout();
		} catch {
			/* session may already be gone */
		}
		clearAuth();
		await goto('/login');
	}
</script>

<div class="sidebar-inner">
	<a href="/" class="brand">Kadence</a>

	<button class="new" onclick={startNew}>+ New chat</button>

	<div class="history">
		<h2 class="heading">History</h2>
		{#if $conversations.length}
			<ul>
				{#each $conversations as c (c.id)}
					<li>
						<a
							href={`/chat/${c.id}`}
							class:active={String(c.id) === $page.params.id}
							onclick={closeSidebar}
						>
							{c.title || 'Untitled'}
						</a>
						<button class="del" aria-label="Delete conversation" onclick={(e) => del(c.id, e)}>×</button>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="empty">No conversations yet</p>
		{/if}
	</div>

	<nav class="links">
		<a href="/documents" onclick={closeSidebar}>Documents</a>
		<a href="/context" onclick={closeSidebar}>Context</a>
		{#if $isAdmin}
			<a href="/admin/users" onclick={closeSidebar}>Users</a>
			<a href="/admin/documents" onclick={closeSidebar}>Public Docs</a>
		{/if}
	</nav>

	<div class="footer">
		{#if $currentUser}<span class="who">{$currentUser.username}</span>{/if}
		<button class="logout" onclick={handleLogout}>Log out</button>
	</div>
</div>

<style>
	.sidebar-inner {
		display: flex;
		flex-direction: column;
		height: 100%;
		padding: 16px 12px;
		gap: 12px;
	}
	.brand {
		font-weight: 700;
		text-decoration: none;
		color: var(--text);
		font-size: 1.1rem;
		padding: 0 4px;
	}
	.new {
		width: 100%;
		padding: 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		cursor: pointer;
		font: inherit;
	}
	.history {
		flex: 1;
		min-height: 0;
		overflow-y: auto;
	}
	.heading {
		font-size: 0.75rem;
		text-transform: uppercase;
		letter-spacing: 0.04em;
		color: var(--text-muted);
		margin: 0 0 8px 4px;
	}
	.empty {
		color: var(--text-muted);
		font-size: 0.9rem;
		padding: 0 4px;
	}
	ul {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		border-radius: var(--radius);
	}
	li a.active {
		background: var(--bg);
	}
	li a {
		flex: 1;
		padding: 8px 10px;
		text-decoration: none;
		color: var(--text);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.del {
		border: none;
		background: none;
		cursor: pointer;
		color: var(--text-muted);
		font-size: 1.1rem;
		padding: 0 8px;
	}
	.links {
		display: flex;
		flex-direction: column;
		gap: 4px;
		border-top: 1px solid var(--border);
		padding-top: 8px;
	}
	.links a {
		text-decoration: none;
		color: var(--text);
		padding: 6px 4px;
	}
	.footer {
		display: flex;
		align-items: center;
		justify-content: space-between;
		border-top: 1px solid var(--border);
		padding-top: 8px;
	}
	.who {
		color: var(--text-muted);
		font-size: 0.9rem;
	}
	.logout {
		background: none;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		padding: 6px 12px;
		cursor: pointer;
		font: inherit;
	}
</style>
