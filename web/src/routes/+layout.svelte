<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import '$lib/styles/app.css';
	import { api, APIError } from '$lib/api/client';
	import { clearAuth, currentUser, isAdmin, isAuthenticated, setAuth } from '$lib/stores/auth';

	let { children } = $props();
	let checking = $state(true);

	function isPublic(path: string): boolean {
		return path === '/login';
	}

	onMount(async () => {
		const path = window.location.pathname;
		try {
			const user = await api.getCurrentUser();
			setAuth(user);
		} catch (err) {
			clearAuth();
			if (err instanceof APIError && err.status === 401 && !isPublic(path)) {
				await goto('/login?returnTo=' + encodeURIComponent(path));
			}
		} finally {
			checking = false;
		}
	});

	async function handleLogout() {
		try {
			await api.logout();
		} catch {
			/* session may already be gone */
		}
		clearAuth();
		await goto('/login');
	}
</script>

{#if checking}
	<div class="loading">Loading…</div>
{:else}
	{#if !isPublic($page.url.pathname)}
		<header class="topbar">
			<a href="/" class="brand">Kadence</a>
			<nav>
				{#if $isAuthenticated}<a href="/chat">Chat</a>{/if}
				{#if $isAdmin}<a href="/admin/users">Users</a>{/if}
				{#if $currentUser}<span class="who">{$currentUser.username}</span>{/if}
				{#if $isAuthenticated}<button class="logout" onclick={handleLogout}>Log out</button>{/if}
			</nav>
		</header>
	{/if}
	<main class="content">
		{@render children()}
	</main>
{/if}

<style>
	.loading { min-height: 100vh; display: grid; place-items: center; color: var(--text-muted); }
	.topbar {
		display: flex; align-items: center; justify-content: space-between;
		padding: 12px 20px; background: var(--surface); border-bottom: 1px solid var(--border);
	}
	.brand { font-weight: 700; text-decoration: none; color: var(--text); }
	nav { display: flex; align-items: center; gap: 16px; }
	.who { color: var(--text-muted); font-size: 0.9rem; }
	.logout { background: none; border: 1px solid var(--border); border-radius: var(--radius); padding: 6px 12px; cursor: pointer; font: inherit; }
	.content { max-width: 900px; margin: 0 auto; padding: 24px 20px; }
</style>
