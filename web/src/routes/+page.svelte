<script lang="ts">
	import { currentUser } from '$lib/stores/auth';
</script>

<section class="home">
	<h1>Welcome{$currentUser ? `, ${$currentUser.username}` : ''} 👋</h1>
	<p class="muted">Your AI coach chat will live here. Coming in a later phase.</p>
</section>

<style>
	.home { text-align: center; padding: 48px 0; }
	.muted { color: var(--text-muted); }
</style>
