<script lang="ts">
	import { onMount } from 'svelte';
	import { canReachServer } from '$lib/stores/connection';
	import { APIError } from '$lib/api/client';
	import Button from '$lib/components/Button.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import {
		listIntegrations,
		startLink,
		unlinkIntegration,
		integrationLabel,
		type Integration
	} from '$lib/api/integrations';

	let integrations = $state<Integration[]>([]);
	let loaded = $state(false);
	let available = $state(true);
	let error = $state('');
	let notice = $state('');
	let busy = $state('');
	let confirming = $state('');

	async function load() {
		try {
			integrations = await listIntegrations();
			available = true;
			error = '';
		} catch (e) {
			// A deployment with no OAuth integration does not mount the endpoint
			// at all. That is a configuration, not a fault, so the section
			// disappears instead of reporting an error nobody can act on.
			//
			// 405 as well as 404: the router still has /api/mcp/{id} for PUT and
			// DELETE, so an unmounted GET on that shape is answered as
			// method-not-allowed rather than not-found.
			if (e instanceof APIError && (e.status === 404 || e.status === 405)) {
				available = false;
				error = '';
			} else {
				error = e instanceof Error ? e.message : 'Could not load integrations.';
			}
		} finally {
			loaded = true;
		}
	}

	// The callback redirects here with the outcome. Read it once, then strip it
	// so a refresh does not repeat a stale banner.
	function readCallbackOutcome() {
		const params = new URLSearchParams(window.location.search);
		const status = params.get('status');
		const server = params.get('integration');
		if (!status) return;

		notice =
			status === 'linked'
				? `${integrationLabel(server ?? '')} is connected.`
				: 'That connection attempt did not complete. Please try again.';

		params.delete('status');
		params.delete('integration');
		const query = params.toString();
		history.replaceState(null, '', window.location.pathname + (query ? `?${query}` : ''));
	}

	onMount(() => {
		readCallbackOutcome();
		void load();
	});

	async function connect(server: string) {
		busy = server;
		try {
			const { authorize_url } = await startLink(server);
			// A full navigation: the flow continues on another origin.
			window.location.assign(authorize_url);
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not start the connection.';
			busy = '';
		}
	}

	async function disconnect(server: string) {
		busy = server;
		confirming = '';
		try {
			await unlinkIntegration(server);
			notice = `${integrationLabel(server)} disconnected. Kadence removed its stored access; your account at the provider is unchanged.`;
			await load();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not disconnect.';
		} finally {
			busy = '';
		}
	}

	function stateLabel(it: Integration): string {
		if (!it.linked) return 'Not connected';
		if (it.status === 'reauth_required') return 'Reconnect needed';
		if (it.status === 'disconnect_pending') return 'Disconnecting';
		return it.scope ? `Connected · ${it.scope}` : 'Connected';
	}
</script>

{#if available}
<section>
	<h2>Integrations</h2>

	{#if notice}
		<p class="notice" role="status">{notice}</p>
	{/if}
	{#if error}
		<p class="error" role="alert">{error}</p>
	{/if}

	{#if !loaded}
		<p class="muted">Loading…</p>
	{:else if integrations.length === 0}
		<p class="muted">No integrations are configured on this server.</p>
	{:else}
		<ul class="integrations">
			{#each integrations as it (it.server)}
				<li>
					<div class="who">
						<span class="name">{integrationLabel(it.server)}</span>
						<span class="state" class:needs-attention={it.status === 'reauth_required'}>
							{stateLabel(it)}
						</span>
					</div>
					{#if it.linked && it.status !== 'reauth_required'}
						<Button
							variant="ghost"
							disabled={!$canReachServer || busy === it.server}
							onclick={() => (confirming = it.server)}
						>
							Disconnect
						</Button>
					{:else}
						<Button
							variant="primary"
							disabled={!$canReachServer || busy === it.server}
							onclick={() => connect(it.server)}
						>
							{it.status === 'reauth_required' ? 'Reconnect' : 'Connect'}
						</Button>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</section>
{/if}

<ConfirmDialog
	open={confirming !== ''}
	title="Disconnect this integration?"
	message="Kadence will delete its stored access for this account. Your data at the provider is not changed, and the provider's own session stays active."
	confirmLabel="Disconnect"
	onConfirm={() => disconnect(confirming)}
	onCancel={() => (confirming = '')}
/>

<style>
	.integrations {
		list-style: none;
		margin: 0;
		padding: 0;
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}

	.integrations li {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 1rem;
	}

	.who {
		display: flex;
		flex-direction: column;
		gap: 0.15rem;
		min-width: 0;
	}

	.name {
		font-weight: 600;
	}

	.state {
		font-size: 0.85rem;
		color: var(--text-muted);
	}

	.state.needs-attention {
		color: var(--warning);
	}

	.muted {
		color: var(--text-muted);
	}

	.error {
		color: var(--danger);
	}
</style>
