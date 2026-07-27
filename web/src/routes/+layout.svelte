<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/stores';
	import '$lib/styles/app.css';
	import { api } from '$lib/api/client';
	import { bootstrapSession } from '$lib/auth/bootstrap';
	import { getOverview } from '$lib/api/context';
	import { listMcp } from '$lib/api/mcp';
	import { clearAuth, setAuth } from '$lib/stores/auth';
	import { PwaLifecycle, type PwaStatus } from '$lib/pwa/lifecycle';
	import { closeSidebar, sidebarOpen, toggleSidebar } from '$lib/stores/ui';
	import Sidebar from '$lib/components/Sidebar.svelte';
	import ReindexStrip from '$lib/components/ReindexStrip.svelte';
	import McpHealthStrip from '$lib/components/McpHealthStrip.svelte';
	import PwaStatusStrip from '$lib/components/PwaStatusStrip.svelte';

	const MOBILE_BREAKPOINT_PX = 900;
	const REINDEX_POLL_INTERVAL_MS = 10000;
	const MCP_POLL_INTERVAL_MS = 10000;

	let { children } = $props();
	let checking = $state(true);
	let reindex = $state({ stale: 0, total: 0 });
	let mcp = $state({ unhealthy: 0, total: 0 });
	let reindexTimer: ReturnType<typeof setInterval> | undefined;
	let mcpTimer: ReturnType<typeof setInterval> | undefined;
	let warnedReindex = false;
	let warnedMcp = false;
	let pwaLifecycle: PwaLifecycle | undefined;
	let pwaStatus = $state<PwaStatus>({
		online: true,
		updateAvailable: false,
		applyingUpdate: false
	});

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
		} catch (err) {
			// Leave the strip hidden on failure (it's a non-critical background
			// poll), but warn once so a silently-broken poll isn't invisible.
			if (!warnedReindex) {
				warnedReindex = true;
				console.warn('reindex status poll failed', err);
			}
		}
	}

	async function refreshMcp(): Promise<void> {
		try {
			const { servers } = await listMcp();
			mcp = {
				unhealthy: servers.filter((s) => s.state === 'unhealthy').length,
				total: servers.length
			};
		} catch (err) {
			// Leave the strip hidden on failure (it's a non-critical background
			// poll), but warn once so a silently-broken poll isn't invisible.
			if (!warnedMcp) {
				warnedMcp = true;
				console.warn('mcp status poll failed', err);
			}
		}
	}

	afterNavigate(() => {
		void pwaLifecycle?.checkForUpdate();
	});

	onMount(async () => {
		if (window.innerWidth < MOBILE_BREAKPOINT_PX) closeSidebar();
		pwaLifecycle = new PwaLifecycle((status) => {
			pwaStatus = status;
		});
		void pwaLifecycle.start();

		navigator.serviceWorker?.addEventListener('message', (e) => {
			if (e.data?.type === 'NAVIGATE' && typeof e.data.url === 'string') {
				void goto(e.data.url);
			}
		});

		const path = window.location.pathname;
		try {
			const session = await bootstrapSession(api.getCurrentUser, { setAuth, clearAuth });
			if (session === 'authenticated') {
				await refreshReindexStatus();
				await refreshMcp();
				reindexTimer = setInterval(refreshReindexStatus, REINDEX_POLL_INTERVAL_MS);
				mcpTimer = setInterval(refreshMcp, MCP_POLL_INTERVAL_MS);
			} else if (session === 'unauthorized' && !isPublic(path)) {
				await goto('/login?returnTo=' + encodeURIComponent(path));
			}
		} finally {
			checking = false;
		}
	});

	onDestroy(() => {
		stopReindexPoll();
		stopMcpPoll();
		pwaLifecycle?.destroy();
	});
</script>

<svelte:window onkeydown={closeSidebarOnEscape} />

{#if checking}
	<div class="loading">Loading…</div>
{:else}
	<div class="app-viewport">
		<PwaStatusStrip
			online={pwaStatus.online}
			updateAvailable={pwaStatus.updateAvailable}
			applyingUpdate={pwaStatus.applyingUpdate}
			onReload={() => pwaLifecycle?.applyUpdate()}
		/>
		{#if isPublic($page.url.pathname)}
			<div class="public-content">{@render children()}</div>
		{:else}
			<ReindexStrip stale={reindex.stale} total={reindex.total} />
			<McpHealthStrip unhealthy={mcp.unhealthy} total={mcp.total} />
			<div class="shell">
				<div class="scrim" class:show={$sidebarOpen} onclick={closeSidebar} aria-hidden="true"></div>
				<aside class="sidebar" class:open={$sidebarOpen}><Sidebar /></aside>
				<div class="main">
					<div class="mobilebar">
						<button class="hamburger" onclick={toggleSidebar} aria-label="Menu">☰</button>
						<a href="/" class="brand-sm">
							<img src="/icons/icon-192.png" alt="" width="24" height="24" />
							<span>Kadence</span>
						</a>
					</div>
					<main>{@render children()}</main>
				</div>
			</div>
		{/if}
	</div>
{/if}

<style>
	.loading { height: 100dvh; display: grid; place-items: center; color: var(--text-muted); }
</style>
