<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import { conversations, newChat, refreshConversations, removeConversation } from '$lib/stores/chat';

	let { children } = $props();

	onMount(refreshConversations);

	function startNew() {
		newChat();
		goto('/chat');
	}
	async function del(id: number, e: Event) {
		e.preventDefault();
		e.stopPropagation();
		await removeConversation(id);
		if (String(id) === $page.params.id) goto('/chat');
	}
</script>

<div class="layout">
	<aside class="sidebar">
		<button class="new" onclick={startNew}>+ New chat</button>
		<ul>
			{#each $conversations as c (c.id)}
				<li class:active={String(c.id) === $page.params.id}>
					<a href={`/chat/${c.id}`}>{c.title || 'Untitled'}</a>
					<button class="del" aria-label="Delete conversation" onclick={(e) => del(c.id, e)}>×</button>
				</li>
			{/each}
		</ul>
	</aside>
	<section class="pane">
		{@render children()}
	</section>
</div>

<style>
	.layout { display: grid; grid-template-columns: 260px 1fr; gap: 16px; height: calc(100vh - 120px); }
	.sidebar { border-right: 1px solid var(--border); padding-right: 12px; overflow-y: auto; }
	.new { width: 100%; padding: 10px; margin-bottom: 12px; border: 1px solid var(--border); border-radius: var(--radius); background: var(--surface); cursor: pointer; font: inherit; }
	ul { list-style: none; margin: 0; padding: 0; }
	li { display: flex; align-items: center; justify-content: space-between; border-radius: var(--radius); }
	li.active { background: var(--bg); }
	li a { flex: 1; padding: 8px 10px; text-decoration: none; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
	.del { border: none; background: none; cursor: pointer; color: var(--text-muted); font-size: 1.1rem; padding: 0 8px; }
	.pane { min-height: 0; }
</style>
