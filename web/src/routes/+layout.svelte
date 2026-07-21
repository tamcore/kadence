<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/stores';
	import '$lib/styles/app.css';
	import { api, APIError } from '$lib/api/client';
	import { getOverview } from '$lib/api/context';
	import { listMcp } from '$lib/api/mcp';
	import { clearAuth, setAuth } from '$lib/stores/auth';
	import { closeSidebar, sidebarOpen, toggleSidebar } from '$lib/stores/ui';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import ReindexStrip from '$lib/components/ReindexStrip.svelte';
	import McpHealthStrip from '$lib/components/McpHealthStrip.svelte';

	const MOBILE_BREAKPOINT_PX = 900;
	const REINDEX_POLL_INTERVAL_MS = 10000;
	const MCP_POLL_INTERVAL_MS = 10000;

	let { children } = $props();
	let checking = $state(true);
	let reindex = $state({ stale: 0, total: 0 });
	let mcp = $state({ unhealthy: 0, total: 0 });
	let reindexTimer: ReturnType<typeof setInterval> | undefined;
	let mcpTimer: ReturnType<typeof setInterval> | undefined;

	function isPublic(path: string): boolean {
		return path === '/login';
	}

	function closeSidebarOnEscape(e: KeyboardEvent): void {
		if (e.key === 'Escape' && $sidebarOpen) closeSidebar();
	}

	function stopReindexPoll(): void {
		if (reindexTimer) {
			clearInterval(reindexTimer);
			reindexTimer = undefined;
		}
	}

	function stopMcpPoll(): void {
		if (mcpTimer) {
			clearInterval(mcpTimer);
			mcpTimer = undefined;
		}
	}

	async function refreshReindexStatus(): Promise<void> {
		try {
			const overview = await getOverview();
			reindex = overview.reindex;
			if (reindex.stale === 0) stopReindexPoll();
		} catch {
			// leave the strip hidden on failure
		}
	}

	async function refreshMcp(): Promise<void> {
		try {
			const { servers } = await listMcp();
			mcp = {
				unhealthy: servers.filter((s) => s.state === 'unhealthy').length,
				total: servers.length
			};
		} catch {
			// leave the strip hidden on failure
		}
	}

	onMount(async () => {
		if (window.innerWidth < MOBILE_BREAKPOINT_PX) closeSidebar();

		const path = window.location.pathname;
		try {
			const user = await api.getCurrentUser();
			setAuth(user);
			await refreshReindexStatus();
			await refreshMcp();
			reindexTimer = setInterval(refreshReindexStatus, REINDEX_POLL_INTERVAL_MS);
			mcpTimer = setInterval(refreshMcp, MCP_POLL_INTERVAL_MS);
		} catch (err) {
			clearAuth();
			if (err instanceof APIError && err.status === 401 && !isPublic(path)) {
				await goto('/login?returnTo=' + encodeURIComponent(path));
			}
		} finally {
			checking = false;
		}
	});

	onDestroy(() => {
		stopReindexPoll();
		stopMcpPoll();
	});
</script>

<svelte:window onkeydown={closeSidebarOnEscape} />

{#if checking}
	<div class="loading">Loading…</div>
{:else if isPublic($page.url.pathname)}
	{@render children()}
{:else}
	<ReindexStrip stale={reindex.stale} total={reindex.total} />
	<McpHealthStrip unhealthy={mcp.unhealthy} total={mcp.total} />
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
