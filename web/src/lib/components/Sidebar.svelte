<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api } from '$lib/api/client';
	import {
		conversations,
		conversationsRefreshError,
		newChat,
		refreshConversations,
		removeConversation,
		renameConversation
	} from '$lib/stores/chat';
	import { clearAuth, currentUser, isAdmin } from '$lib/stores/auth';
	import { refreshScheduled, scheduledUnreadCount } from '$lib/stores/scheduled';
	import { closeSidebar } from '$lib/stores/ui';
	import { onMount } from 'svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import Input from '$lib/components/Input.svelte';
	import Button from '$lib/components/Button.svelte';

	// The sidebar is global, so load history on every shell route (incl. home)
	// — the start page greets with the conversation history.
	onMount(() => {
		void refreshConversations();
		if ($currentUser?.scheduledEnabled) void refreshScheduled();
	});

	let deleteTargetId = $state<string | null>(null);
	let renameTargetId = $state<string | null>(null);
	let renameValue = $state('');
	let renameError = $state('');

	function startNew(): void {
		newChat();
		goto('/');
		closeSidebar();
	}

	function requestDelete(id: string, e: Event): void {
		e.preventDefault();
		e.stopPropagation();
		deleteTargetId = id;
	}

	async function confirmDelete(): Promise<void> {
		const id = deleteTargetId;
		deleteTargetId = null;
		if (!id) return;
		const wasActive = id === $page.params.id;
		await removeConversation(id);
		if (wasActive) {
			goto('/');
			closeSidebar();
		}
	}

	function requestRename(id: string, currentTitle: string, e: Event): void {
		e.preventDefault();
		e.stopPropagation();
		renameTargetId = id;
		renameValue = currentTitle;
		renameError = '';
	}

	function closeRename(): void {
		renameTargetId = null;
		renameError = '';
	}

	async function confirmRename(e: SubmitEvent): Promise<void> {
		e.preventDefault();
		const id = renameTargetId;
		if (!id) return;
		const title = renameValue.trim();
		if (!title) return;
		try {
			await renameConversation(id, title);
			closeRename();
		} catch (err) {
			renameError = err instanceof Error ? err.message : 'Could not rename conversation';
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
		{#if $conversationsRefreshError}
			<p class="refresh-hint">Couldn't refresh conversations</p>
		{/if}
		{#if $conversations.length}
			<ul>
				{#each $conversations as c (c.id)}
					<li>
						<a
							href={`/chat/${c.id}`}
							class:active={c.id === $page.params.id}
							onclick={closeSidebar}
						>
							{c.title || 'Untitled'}
						</a>
						<button
							class="rename"
							aria-label="Rename conversation"
							onclick={(e) => requestRename(c.id, c.title, e)}
						>
							✎
						</button>
						<button class="del" aria-label="Delete conversation" onclick={(e) => requestDelete(c.id, e)}>×</button>
					</li>
				{/each}
			</ul>
		{:else}
			<p class="empty">No conversations yet</p>
		{/if}
	</div>

	<nav class="links">
		{#if $currentUser?.scheduledEnabled}
			<a
				href="/scheduled"
				class:active={$page.url.pathname.startsWith('/scheduled')}
				aria-current={$page.url.pathname.startsWith('/scheduled') ? 'page' : undefined}
				onclick={closeSidebar}
			>
				<span>Scheduled</span>
				{#if $scheduledUnreadCount > 0}
					<span
						class="unread"
						aria-label={`${$scheduledUnreadCount} unread scheduled results`}
					>{$scheduledUnreadCount}</span>
				{/if}
			</a>
		{/if}
		<a href="/documents" onclick={closeSidebar}>Documents</a>
		<a href="/context" onclick={closeSidebar}>Context</a>
		<a href="/mcp" onclick={closeSidebar}>MCP</a>
		{#if $isAdmin}
			<a href="/admin/users" onclick={closeSidebar}>Users</a>
			<a href="/admin/documents" onclick={closeSidebar}>Public Docs</a>
		{/if}
	</nav>

	<div class="footer">
		{#if $currentUser}<span class="who">{$currentUser.username}</span>{/if}
		<a href="/profile" class="profile-link" onclick={closeSidebar}>Profile</a>
		<button class="logout" onclick={handleLogout}>Log out</button>
	</div>
</div>

<ConfirmDialog
	open={deleteTargetId !== null}
	title="Delete conversation"
	message="Delete this conversation? This cannot be undone."
	onConfirm={confirmDelete}
	onCancel={() => (deleteTargetId = null)}
/>

<Modal open={renameTargetId !== null} title="Rename conversation" onClose={closeRename}>
	<form class="rename-form" onsubmit={confirmRename}>
		{#if renameError}<div class="error" role="alert">{renameError}</div>{/if}
		<Input label="Title" name="title" required bind:value={renameValue} />
		<div class="rename-actions">
			<Button type="button" variant="ghost" onclick={closeRename}>Cancel</Button>
			<Button type="submit" variant="primary">Save</Button>
		</div>
	</form>
</Modal>

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
	.rename {
		border: none;
		background: none;
		cursor: pointer;
		color: var(--text-muted);
		font-size: 0.95rem;
		padding: 0 6px;
	}
	.del {
		border: none;
		background: none;
		cursor: pointer;
		color: var(--text-muted);
		font-size: 1.1rem;
		padding: 0 8px;
	}
	.refresh-hint {
		color: var(--text-muted);
		font-size: 0.8rem;
		padding: 0 4px 6px;
		margin: 0;
	}
	.rename-form {
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.rename-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}
	.error {
		color: var(--danger);
		font-size: 0.85rem;
	}
	.links {
		display: flex;
		flex-direction: column;
		gap: 4px;
		border-top: 1px solid var(--border);
		padding-top: 8px;
	}
	.links a {
		display: flex;
		align-items: center;
		justify-content: space-between;
		text-decoration: none;
		color: var(--text);
		padding: 6px 4px;
	}
	.links a.active {
		color: var(--accent);
		font-weight: 600;
	}
	.unread {
		display: inline-grid;
		min-width: 1.35rem;
		height: 1.35rem;
		place-items: center;
		border-radius: 999px;
		background: var(--accent);
		color: #fff;
		font: 600 0.72rem/1 ui-monospace, SFMono-Regular, Consolas, monospace;
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
	.profile-link {
		color: var(--text);
		text-decoration: none;
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
