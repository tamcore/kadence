<script lang="ts">
	import { onMount } from 'svelte';
	import { refreshConversations } from '$lib/stores/chat';

	let { children } = $props();

	onMount(refreshConversations);
</script>

{@render children()}
