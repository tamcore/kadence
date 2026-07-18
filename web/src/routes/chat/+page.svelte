<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { newChat } from '$lib/stores/chat';
	import ChatView from '$lib/components/ChatView.svelte';

	onMount(newChat);

	function onNewConversation(id: number) {
		// Make the URL shareable without remounting mid-stream (the store owns the stream).
		goto(`/chat/${id}`, { replaceState: true, noScroll: true, keepFocus: true });
	}
</script>

<ChatView {onNewConversation} />
