<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Composer from '$lib/components/Composer.svelte';
	import { currentUser } from '$lib/stores/auth';
	import { newChat, sendMessage, sending } from '$lib/stores/chat';

	onMount(() => newChat()); // fresh state on landing

	async function start(text: string): Promise<void> {
		const id = await sendMessage(text); // streams; meta event sets activeId
		if (id != null) await goto(`/chat/${id}`); // seamless: store already holds the live stream
	}
</script>

<section class="home">
	<h1>What can I help with{$currentUser ? `, ${$currentUser.username}` : ''}?</h1>
	<div class="composer-wrap">
		<Composer disabled={$sending} onSubmit={start} placeholder="Ask your coach…" />
	</div>
</section>

<style>
	.home {
		max-width: 720px;
		margin: 0 auto;
		min-height: 100%;
		display: flex;
		flex-direction: column;
		justify-content: center;
		align-items: stretch;
		gap: 24px;
		padding: 48px 16px;
	}
	h1 {
		text-align: center;
		font-size: 1.75rem;
		margin: 0;
	}
	.composer-wrap {
		width: 100%;
	}
</style>
