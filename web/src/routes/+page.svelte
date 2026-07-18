<script lang="ts">
	import { currentUser } from '$lib/stores/auth';
</script>

<section class="home">
	<h1>Welcome{$currentUser ? `, ${$currentUser.username}` : ''} 👋</h1>
	<p class="muted">Chat with your AI coach.</p>
	<a class="cta" href="/chat">Open chat →</a>
</section>

<style>
	.home { text-align: center; padding: 48px 0; }
	.muted { color: var(--text-muted); }
	.cta { display: inline-block; margin-top: 16px; padding: 10px 20px; background: var(--accent); color: #fff; border-radius: var(--radius); text-decoration: none; font-weight: 600; }
</style>
