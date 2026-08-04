<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { api } from '$lib/api/client';
	import type { Conversation } from '$lib/types';
	import {
		conversations,
		conversationsRefreshError,
		newChat,
		pinConversation,
		refreshConversations,
		removeConversation,
		renameConversation
	} from '$lib/stores/chat';
	import { clearAuth, currentUser, isAdmin } from '$lib/stores/auth';
	import { canReachServer } from '$lib/stores/connection';
	import { refreshScheduled, scheduledUnreadCount } from '$lib/stores/scheduled';
	import { closeSidebar } from '$lib/stores/ui';
	import { onMount } from 'svelte';
	import ActionMenu, { type ActionMenuItem, type ActionMenuTrigger } from '$lib/components/ActionMenu.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import Modal from '$lib/components/Modal.svelte';
	import Input from '$lib/components/Input.svelte';
	import Button from '$lib/components/Button.svelte';
	import ThemeCycleButton from '$lib/components/ThemeCycleButton.svelte';

	const PINNED_EXPANDED_KEY = 'kadence_sidebar_pinned_expanded';
	const RECENTS_EXPANDED_KEY = 'kadence_sidebar_recents_expanded';
	const pinnedSectionId = 'sidebar-pinned-conversations';
	const recentSectionId = 'sidebar-recent-conversations';

	let deleteTargetId = $state<string | null>(null);
	let renameTargetId = $state<string | null>(null);
	let renameValue = $state('');
	let renameError = $state('');
	let actionError = $state('');
	let pinnedExpanded = $state(true);
	let recentsExpanded = $state(true);

	let pinned = $derived($conversations.filter((conversation) => conversation.pinnedAt !== null));
	let recents = $derived($conversations.filter((conversation) => conversation.pinnedAt === null));
	let userName = $derived($currentUser?.displayName || $currentUser?.username || 'Account');
	let initials = $derived(userName.split(/\s+/).map((part) => part[0]).join('').slice(0, 2).toUpperCase());

	onMount(() => {
		pinnedExpanded = window.localStorage.getItem(PINNED_EXPANDED_KEY) !== 'false';
		recentsExpanded = window.localStorage.getItem(RECENTS_EXPANDED_KEY) !== 'false';
		void refreshConversations();
		if ($currentUser?.scheduledEnabled) void refreshScheduled();
	});

	function startNew(): void {
		newChat();
		goto('/');
		closeSidebar();
	}

	function requestDelete(id: string): void {
		deleteTargetId = id;
	}

	async function confirmDelete(): Promise<void> {
		const id = deleteTargetId;
		deleteTargetId = null;
		if (!id) return;
		const wasActive = id === $page.params.id;
		try {
			await removeConversation(id);
			if (wasActive) {
				goto('/');
				closeSidebar();
			}
		} catch (error) {
			actionError = error instanceof Error ? error.message : 'Could not delete conversation';
		}
	}

	function requestRename(id: string, currentTitle: string): void {
		renameTargetId = id;
		renameValue = currentTitle;
		renameError = '';
	}

	function closeRename(): void {
		renameTargetId = null;
		renameError = '';
	}

	async function confirmRename(event: SubmitEvent): Promise<void> {
		event.preventDefault();
		const id = renameTargetId;
		if (!id) return;
		const title = renameValue.trim();
		if (!title) return;
		try {
			await renameConversation(id, title);
			closeRename();
		} catch (error) {
			renameError = error instanceof Error ? error.message : 'Could not rename conversation';
		}
	}

	async function togglePinned(conversation: Conversation): Promise<void> {
		actionError = '';
		try {
			await pinConversation(conversation.id, conversation.pinnedAt === null);
		} catch (error) {
			actionError = error instanceof Error ? error.message : 'Could not update conversation';
		}
	}

	function conversationItems(conversation: Conversation): ActionMenuItem[] {
		const pinLabel = conversation.pinnedAt === null ? 'Pin' : 'Unpin';
		return [
			{ label: 'Share', ariaLabel: 'Share (coming soon)', disabled: true },
			{ label: 'Rename', onSelect: () => requestRename(conversation.id, conversation.title) },
			{ separator: true },
			{ label: pinLabel, onSelect: () => togglePinned(conversation) },
			{ label: 'Archive', ariaLabel: 'Archive (coming soon)', disabled: true },
			{ separator: true },
			{ label: 'Delete', danger: true, onSelect: () => requestDelete(conversation.id) }
		];
	}

	function toggleSection(section: 'pinned' | 'recents'): void {
		if (section === 'pinned') {
			pinnedExpanded = !pinnedExpanded;
			window.localStorage.setItem(PINNED_EXPANDED_KEY, String(pinnedExpanded));
			return;
		}
		recentsExpanded = !recentsExpanded;
		window.localStorage.setItem(RECENTS_EXPANDED_KEY, String(recentsExpanded));
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
	<header class="sidebar-header">
		<a href="/" class="brand">
			<img src="/icons/icon-192.png" alt="" width="24" height="24" />
			<span>Kadence</span>
		</a>
	</header>

	<div class="sidebar-scroll">
		<button class="new" onclick={startNew}>
			<svg aria-hidden="true" viewBox="0 0 16 16"><path d="M8 3v10M3 8h10" /></svg>
			New chat
		</button>

		<nav class="links" aria-label="Workspace">
			{#if $currentUser?.scheduledEnabled}
				<a
					href="/scheduled"
					class:active={$page.url.pathname.startsWith('/scheduled')}
					aria-current={$page.url.pathname.startsWith('/scheduled') ? 'page' : undefined}
					onclick={closeSidebar}
				>
					<span>Scheduled</span>
					{#if $scheduledUnreadCount > 0}
						<span class="unread" aria-label={`${$scheduledUnreadCount} unread scheduled results`}>{$scheduledUnreadCount}</span>
					{/if}
				</a>
			{/if}
			<a href="/documents" onclick={closeSidebar}>Documents</a>
			<a href="/context" onclick={closeSidebar}>Context</a>
			<a href="/mcp" onclick={closeSidebar}>MCP</a>
			{#if $isAdmin}
				<a href="/admin/users" onclick={closeSidebar}>Users</a>
				<a href="/admin/documents" onclick={closeSidebar}>Public Docs</a>
				<a href="/admin/mcp-audit" onclick={closeSidebar}>MCP Audit</a>
			{/if}
		</nav>

		{#if $conversationsRefreshError}
			<p class="refresh-hint" role="status">Couldn't refresh conversations</p>
		{/if}
		{#if actionError}
			<p class="action-error" role="status">{actionError}</p>
		{/if}

		{#if pinned.length}
			<section class="conversation-section">
				<button
					class="section-toggle"
					aria-expanded={pinnedExpanded}
					aria-controls={pinnedSectionId}
					onclick={() => toggleSection('pinned')}
				>
					<span>Pinned</span><span aria-hidden="true">{pinnedExpanded ? '⌄' : '›'}</span>
				</button>
				{#if pinnedExpanded}
					<div id={pinnedSectionId}>
						<ul class="conversation-list">
							{#each pinned as conversation (conversation.id)}
								{@render conversationRow(conversation)}
							{/each}
						</ul>
					</div>
				{/if}
			</section>
		{/if}

		<section class="conversation-section">
			<button
				class="section-toggle"
				aria-expanded={recentsExpanded}
				aria-controls={recentSectionId}
				onclick={() => toggleSection('recents')}
			>
				<span>Recents</span><span aria-hidden="true">{recentsExpanded ? '⌄' : '›'}</span>
			</button>
			{#if recentsExpanded}
				<div id={recentSectionId}>
					{#if recents.length}
						<ul class="conversation-list">
							{#each recents as conversation (conversation.id)}
								{@render conversationRow(conversation)}
							{/each}
						</ul>
					{:else if !pinned.length}
						<p class="empty">No conversations yet</p>
					{/if}
				</div>
			{/if}
		</section>
	</div>

	<footer class="sidebar-footer">
		<div class="footer-account">
			<ActionMenu
				label="Account actions"
				items={[
					{ label: 'Profile', href: '/profile', onSelect: closeSidebar },
					{ separator: true },
					{ label: 'Log out', danger: true, onSelect: handleLogout }
				]}
			>
				{#snippet trigger(menu: ActionMenuTrigger)}
					<button
						type="button"
						class="account-trigger"
						data-action-menu-trigger
						aria-label="Account actions"
						aria-haspopup="menu"
						aria-expanded={menu.expanded}
						aria-controls={menu.expanded ? menu.menuId : undefined}
						onclick={menu.open}
					>
						<span class="avatar" aria-hidden="true">{initials}</span>
						<span class="account-name">{userName}</span>
						<svg aria-hidden="true" viewBox="0 0 16 16"><circle cx="3" cy="8" r="1" /><circle cx="8" cy="8" r="1" /><circle cx="13" cy="8" r="1" /></svg>
					</button>
				{/snippet}
			</ActionMenu>
		</div>
		<ThemeCycleButton />
	</footer>
</div>

{#snippet conversationRow(conversation: Conversation)}
	<li class:active={conversation.id === $page.params.id}>
		<a href={`/chat/${conversation.id}`} class:active={conversation.id === $page.params.id} onclick={closeSidebar}>
			{conversation.title || 'Untitled'}
		</a>
		<div class="row-actions">
			<button
				type="button"
				class="icon-button pin-action"
				aria-label={conversation.pinnedAt === null ? 'Pin conversation' : 'Unpin conversation'}
				onclick={() => void togglePinned(conversation)}
			>
				<svg aria-hidden="true" viewBox="0 0 16 16"><path d="m5 2 6 0 .7 4.3 1.8 1.8H9l-1 5-1-5H2.5l1.8-1.8z" /></svg>
			</button>
			<ActionMenu label={`${conversation.title || 'Untitled'} actions`} items={conversationItems(conversation)}>
				{#snippet trigger(menu: ActionMenuTrigger)}
					<button
						type="button"
						class="icon-button"
						data-action-menu-trigger
						aria-label={`${conversation.title || 'Untitled'} actions`}
						aria-haspopup="menu"
						aria-expanded={menu.expanded}
						aria-controls={menu.expanded ? menu.menuId : undefined}
						onclick={menu.open}
					>
						<svg aria-hidden="true" viewBox="0 0 16 16"><circle cx="3" cy="8" r="1" /><circle cx="8" cy="8" r="1" /><circle cx="13" cy="8" r="1" /></svg>
					</button>
				{/snippet}
			</ActionMenu>
		</div>
	</li>
{/snippet}

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
			<Button type="submit" variant="primary" disabled={!$canReachServer}>Save</Button>
		</div>
	</form>
</Modal>

<style>
	.sidebar-inner { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--surface); }
	.sidebar-header, .sidebar-footer { flex: 0 0 auto; padding: 16px 12px; }
	.sidebar-header { border-bottom: 1px solid var(--border); }
	.brand { display: flex; align-items: center; gap: 8px; color: var(--text); font-size: 1.1rem; font-weight: 700; padding: 0 4px; text-decoration: none; }
	.brand img { border-radius: 5px; flex: 0 0 auto; }
	.sidebar-scroll { display: flex; flex: 1; flex-direction: column; gap: 16px; min-width: 0; min-height: 0; overflow-x: hidden; overflow-y: auto; overscroll-behavior-y: contain; padding: 12px; }
	.new { display: flex; align-items: center; justify-content: center; gap: 7px; width: 100%; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); cursor: pointer; font: inherit; padding: 10px; }
	.new:hover { background: var(--bg); }
	svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.5; }
	.links { display: flex; flex-direction: column; gap: 2px; }
	.links a { display: flex; align-items: center; justify-content: space-between; border-radius: 6px; color: var(--text); padding: 7px 8px; text-decoration: none; }
	.links a:hover, .links a.active { background: var(--bg); color: var(--accent); font-weight: 600; }
	.unread { display: inline-grid; min-width: 1.35rem; height: 1.35rem; place-items: center; border-radius: 999px; background: var(--accent); color: var(--on-accent); font: 600 .72rem/1 ui-monospace, SFMono-Regular, Consolas, monospace; }
	.conversation-section { display: grid; gap: 6px; min-width: 0; }
	.conversation-section > div, .section-toggle, .conversation-list { min-width: 0; }
	.section-toggle { display: flex; align-items: center; justify-content: space-between; width: 100%; border: 0; background: transparent; color: var(--text-muted); cursor: pointer; font: 650 .72rem/1 var(--font); letter-spacing: .06em; padding: 7px 6px; text-align: left; text-transform: uppercase; }
	.conversation-list { overflow: hidden; list-style: none; margin: 0; padding: 0; }
	.conversation-list li { position: relative; display: flex; align-items: center; min-width: 0; border-radius: 6px; }
	.conversation-list li:hover, .conversation-list li:focus-within, .conversation-list li.active { background: var(--bg); }
	.conversation-list a { display: block; width: 100%; min-width: 0; overflow: hidden; color: var(--text); padding: 8px; text-decoration: none; text-overflow: ellipsis; white-space: nowrap; }
	.conversation-list a.active { font-weight: 650; }
	.row-actions { position: absolute; top: 0; right: 0; bottom: 0; z-index: 1; display: flex; align-items: center; background: var(--surface); opacity: 0; pointer-events: none; transition: opacity .12s ease; }
	.conversation-list li.active .row-actions { background: var(--bg); }
	.conversation-list li:is(:hover, :focus-within, :has([aria-expanded="true"])) .row-actions { background: var(--bg); opacity: 1; pointer-events: auto; }
	.icon-button { display: grid; width: 30px; height: 30px; place-items: center; border: 0; border-radius: 5px; background: transparent; color: var(--text-muted); cursor: pointer; padding: 0; }
	.icon-button:hover, .icon-button:focus-visible { background: var(--surface); color: var(--text); }
	.icon-button svg { width: 15px; height: 15px; }
	.icon-button svg circle, .account-trigger svg circle { fill: currentColor; stroke: none; }
	.empty, .refresh-hint, .action-error { margin: 0; color: var(--text-muted); font-size: .85rem; padding: 2px 8px; }
	.action-error { color: var(--danger); }
	.sidebar-footer { border-top: 1px solid var(--border); display: flex; align-items: center; gap: 8px; }
	.footer-account { flex: 1; min-width: 0; }
	.account-trigger { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; gap: 8px; width: 100%; border: 0; border-radius: 6px; background: transparent; color: var(--text); cursor: pointer; font: inherit; padding: 4px; text-align: left; }
	.account-trigger:hover, .account-trigger:focus-visible { background: var(--bg); }
	.avatar { display: grid; width: 29px; height: 29px; place-items: center; border-radius: 50%; background: color-mix(in srgb, var(--accent) 18%, var(--surface)); color: var(--accent); font-size: .72rem; font-weight: 700; }
	.account-name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.account-trigger svg { width: 18px; height: 18px; }
	.rename-form { display: flex; flex-direction: column; gap: 12px; }
	.rename-actions { display: flex; justify-content: flex-end; gap: 8px; }
	.error { color: var(--danger); font-size: .85rem; }
	@media (hover: none), (pointer: coarse) {
		.conversation-list { overflow: visible; }
		.row-actions { opacity: 1; pointer-events: auto; }
		.row-actions .pin-action { display: none; }
	}
</style>
