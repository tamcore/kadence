<script lang="ts">
	import { page } from '$app/stores';
	import { loadConversation } from '$lib/stores/chat';
	import ChatView from '$lib/components/ChatView.svelte';

	// Load whenever the route id changes (client-side nav + direct URL open).
	$effect(() => {
		const id = $page.params.id;
		if (id) void loadConversation(id);
	});
</script>

<ChatView />
