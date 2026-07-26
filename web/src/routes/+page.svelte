<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import Composer from '$lib/components/Composer.svelte';
	import { currentUser } from '$lib/stores/auth';
	import { activeId, newChat, sendMessage, sending } from '$lib/stores/chat';
	import type { Document } from '$lib/types';

	onMount(() => newChat()); // fresh state on landing

	// Navigate to /chat/[id] as soon as the conversation id is known (on the
	// `meta` stream event), not after the whole stream completes — the stream
	// keeps rendering inside /chat/[id] since it isn't tied to this component.
	function start(
		text: string,
		files: File[] = [],
		documentReferences: Document[] = []
	): Promise<boolean> {
		let unsubscribe = () => {};
		unsubscribe = activeId.subscribe((id) => {
			if (id != null) {
				void goto(`/chat/${id}`);
				queueMicrotask(() => unsubscribe());
			}
		});
		const pending =
			files.length > 0 || documentReferences.length > 0
				? sendMessage(text, files, documentReferences)
				: sendMessage(text);
		return pending.then((id) => {
			if (id == null) unsubscribe();
			return id != null;
		});
	}
</script>

<section class="home">
	<h1>
		What can I help with{$currentUser ? `, ${$currentUser.displayName || $currentUser.username}` : ''}?
	</h1>
	<div class="composer-wrap">
		<Composer richInput disabled={$sending} onSubmit={start} placeholder="Ask your coach…" />
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
