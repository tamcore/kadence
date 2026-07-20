<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import '$lib/styles/app.css';
	import { api, APIError } from '$lib/api/client';
	import { clearAuth, setAuth } from '$lib/stores/auth';
	import { closeSidebar, sidebarOpen, toggleSidebar } from '$lib/stores/ui';
	import Sidebar from '$lib/components/Sidebar.svelte';

	const MOBILE_BREAKPOINT_PX = 900;

	let { children } = $props();
	let checking = $state(true);

	function isPublic(path: string): boolean {
		return path === '/login';
	}

	function closeSidebarOnEscape(e: KeyboardEvent): void {
		if (e.key === 'Escape' && $sidebarOpen) closeSidebar();
	}

	onMount(async () => {
		if (window.innerWidth < MOBILE_BREAKPOINT_PX) closeSidebar();

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
</script>

<svelte:window onkeydown={closeSidebarOnEscape} />

{#if checking}
	<div class="loading">Loading…</div>
{:else if isPublic($page.url.pathname)}
	{@render children()}
{:else}
	<div class="shell">
		<div class="scrim" class:show={$sidebarOpen} onclick={closeSidebar} aria-hidden="true"></div>
		<aside class="sidebar" class:open={$sidebarOpen}><Sidebar /></aside>
		<div class="main">
			<div class="mobilebar">
				<button class="hamburger" onclick={toggleSidebar} aria-label="Menu">☰</button>
				<span class="brand-sm">Kadence</span>
			</div>
			<main>{@render children()}</main>
		</div>
	</div>
{/if}

<style>
	.loading { min-height: 100vh; display: grid; place-items: center; color: var(--text-muted); }
</style>
