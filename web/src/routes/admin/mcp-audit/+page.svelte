<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { goto } from '$app/navigation';
	import { isAdmin } from '$lib/stores/auth';
	import ActionMenu from '$lib/components/ActionMenu.svelte';
	import {
		getMcpAuditCall,
		listMcpAuditCalls,
		type McpAuditDetail,
		type McpAuditFilters,
		type McpAuditSummary
	} from '$lib/api/mcpAudit';

	let calls = $state<McpAuditSummary[]>([]);
	let selected = $state<McpAuditDetail | null>(null);
	let nextCursor = $state('');
	let loading = $state(true);
	let loadingMore = $state(false);
	let detailLoading = $state(false);
	let error = $state('');
	let listRequest = 0;
	let detailRequest = 0;

	let userId = $state('');
	let conversationId = $state('');
	let source = $state('');
	let status = $state('');
	let model = $state('');
	let tool = $state('');
	let from = $state('');
	let to = $state('');

	function activeFilters(cursor = ''): McpAuditFilters {
		const filters: McpAuditFilters = { limit: 50 };
		if (userId) filters.userId = Number(userId);
		if (conversationId) filters.conversationId = conversationId.trim();
		if (source === 'chat' || source === 'scheduled') filters.source = source;
		if (status === 'running' || status === 'succeeded' || status === 'failed') {
			filters.status = status;
		}
		if (model) filters.model = model.trim();
		if (tool) filters.tool = tool.trim();
		if (from) filters.from = new Date(from).toISOString();
		if (to) filters.to = new Date(to).toISOString();
		if (cursor) filters.cursor = cursor;
		return filters;
	}

	async function load(reset = true) {
		const request = ++listRequest;
		if (reset) {
			closeDetail();
			loading = true;
		}
		else loadingMore = true;
		error = '';
		try {
			const page = await listMcpAuditCalls(activeFilters(reset ? '' : nextCursor));
			if (request !== listRequest) return;
			calls = reset ? page.items : [...calls, ...page.items];
			nextCursor = page.nextCursor ?? '';
			if (reset) selected = null;
		} catch (cause) {
			if (request !== listRequest) return;
			error = cause instanceof Error ? cause.message : 'Could not load MCP audit calls';
		} finally {
			if (request === listRequest) {
				loading = false;
				loadingMore = false;
			}
		}
	}

	async function openDetail(id: number) {
		const request = ++detailRequest;
		detailLoading = true;
		error = '';
		try {
			const detail = await getMcpAuditCall(id);
			if (request === detailRequest) selected = detail;
		} catch (cause) {
			if (request !== detailRequest) return;
			error = cause instanceof Error ? cause.message : 'Could not load MCP audit call';
		} finally {
			if (request === detailRequest) detailLoading = false;
		}
	}

	function closeDetail() {
		detailRequest++;
		detailLoading = false;
		selected = null;
	}

	function applyFilters(event: SubmitEvent) {
		event.preventDefault();
		void load();
	}

	function resetFilters() {
		userId = '';
		conversationId = '';
		source = '';
		status = '';
		model = '';
		tool = '';
		from = '';
		to = '';
		void load();
	}

	function formatTimestamp(value: string): string {
		return new Intl.DateTimeFormat(undefined, {
			dateStyle: 'short',
			timeStyle: 'medium'
		}).format(new Date(value));
	}

	function duration(call: McpAuditSummary): string {
		if (!call.finishedAt) return 'in progress';
		const milliseconds = new Date(call.finishedAt).getTime() - new Date(call.startedAt).getTime();
		if (milliseconds < 1000) return `${milliseconds} ms`;
		return `${(milliseconds / 1000).toFixed(2)} s`;
	}

	onMount(() => {
		if (!get(isAdmin)) {
			goto('/');
			return;
		}
		void load();
	});
</script>

<svelte:head><title>MCP audit · Kadence</title></svelte:head>

<div class="page audit-page">
	<header class="page-header">
		<div>
			<p class="eyebrow">Administration / observability</p>
			<h1>MCP audit</h1>
			<p class="muted">Remote tool calls, actor context, model attribution, and retained payloads.</p>
		</div>
		<button class="refresh" type="button" onclick={() => load()}>Refresh</button>
	</header>

	<form class="filters" onsubmit={applyFilters}>
		<label>
			<span>Tool</span>
			<input bind:value={tool} placeholder="server__tool" />
		</label>
		<label>
			<span>Model</span>
			<input bind:value={model} placeholder="model name" />
		</label>
		<label>
			<span>User ID</span>
			<input bind:value={userId} type="number" min="1" placeholder="Any" />
		</label>
		<label>
			<span>Source</span>
			<select bind:value={source}>
				<option value="">Any</option>
				<option value="chat">Chat</option>
				<option value="scheduled">Scheduled</option>
			</select>
		</label>
		<label>
			<span>Status</span>
			<select bind:value={status}>
				<option value="">Any</option>
				<option value="succeeded">Succeeded</option>
				<option value="failed">Failed</option>
				<option value="running">Running</option>
			</select>
		</label>
		<label class="conversation-filter">
			<span>Chat ID</span>
			<input bind:value={conversationId} placeholder="Conversation UUID" />
		</label>
		<label>
			<span>From</span>
			<input bind:value={from} type="datetime-local" />
		</label>
		<label>
			<span>To</span>
			<input bind:value={to} type="datetime-local" />
		</label>
		<div class="filter-actions">
			<button class="apply" type="submit">Apply filters</button>
			<button class="clear" type="button" onclick={resetFilters}>Clear</button>
		</div>
	</form>

	{#if error}<div class="error" role="alert">{error}</div>{/if}

	<div class:with-detail={selected !== null || detailLoading} class="trace-layout">
		<section class="trace-list" aria-label="MCP call audit records">
			{#if loading}
				<p class="state">Loading audit trail…</p>
			{:else if calls.length === 0}
				<p class="state">No calls match current filters.</p>
			{:else}
				<div class="table-wrap">
					<table aria-label="MCP audit calls">
						<thead>
							<tr>
								<th>Started</th>
								<th>Status</th>
								<th>Tool</th>
								<th>Actor</th>
								<th>Model</th>
								<th>Duration</th>
								<th><span class="sr-only">Details</span></th>
							</tr>
						</thead>
						<tbody>
							{#each calls as call (call.id)}
								<tr
									class:selected={selected?.id === call.id}
									aria-label={`${call.toolName} audit call`}
								>
									<td class="timestamp">
										<span class="mobile-label">Started</span>
										<span>{formatTimestamp(call.startedAt)}</span>
									</td>
									<td>
										<span class="mobile-label">Status</span>
										<span class="status {call.status}">{call.status}</span>
									</td>
									<td class="tool-cell">
										<span class="mobile-label">Tool</span>
										<span class="tool-value">
											<strong>{call.toolName}</strong>
											<span class="source-row">
												<span class="mobile-label">Source</span>
												<span class="source">{call.source}</span>
											</span>
										</span>
									</td>
									<td>
										<span class="mobile-label">Actor</span>
										<span>{call.actorUsername || `User ${call.actorUserId}`}</span>
									</td>
									<td class="model">
										<span class="mobile-label">Model</span>
										<span>{call.model}</span>
									</td>
									<td>
										<span class="mobile-label">Duration</span>
										<span>{duration(call)}</span>
									</td>
									<td class="actions-cell">
										<button
											class="inspect"
											type="button"
											aria-label={`View call ${call.id}`}
											onclick={() => openDetail(call.id)}
										>View</button>
										<span class="mobile-actions">
											<ActionMenu
												label={`Actions for call ${call.id}`}
												items={[{ label: 'View', onSelect: () => openDetail(call.id) }]}
											/>
										</span>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
				{#if nextCursor}
					<button class="load-more" type="button" disabled={loadingMore} onclick={() => load(false)}>
						{loadingMore ? 'Loading…' : 'Load older calls'}
					</button>
				{/if}
			{/if}
		</section>

		{#if detailLoading}
			<aside class="detail"><p class="state">Loading trace…</p></aside>
		{:else if selected}
			<aside class="detail" aria-label={`MCP call ${selected.id} detail`}>
				<div class="detail-head">
					<div>
						<span class="status {selected.status}">{selected.status}</span>
						<h2>{selected.toolName}</h2>
					</div>
					<button class="close" type="button" aria-label="Close detail" onclick={closeDetail}>×</button>
				</div>

				<dl class="metadata">
					<div><dt>Call</dt><dd>#{selected.id} · {selected.toolCallId}</dd></div>
					<div><dt>Actor</dt><dd>{selected.actorUsername} · user {selected.actorUserId}</dd></div>
					<div><dt>Model</dt><dd>{selected.model}</dd></div>
					<div><dt>Source</dt><dd>{selected.source}</dd></div>
					<div><dt>Chat ID</dt><dd>{selected.conversationId}</dd></div>
					{#if selected.scheduledTaskId}
						<div><dt>Scheduled task</dt><dd>{selected.scheduledTaskId}</dd></div>
					{/if}
					<div><dt>Started</dt><dd>{formatTimestamp(selected.startedAt)}</dd></div>
					<div><dt>Duration</dt><dd>{duration(selected)}</dd></div>
				</dl>

				<section class="payload">
					<h3>Arguments</h3>
					<pre>{selected.arguments || '—'}</pre>
				</section>
				{#if selected.error}
					<section class="payload failure">
						<h3>Error</h3>
						<pre>{selected.error}</pre>
					</section>
				{:else}
					<section class="payload">
						<h3>Result</h3>
						<pre>{selected.result || '—'}</pre>
					</section>
				{/if}
			</aside>
		{/if}
	</div>
</div>

<style>
	.audit-page { max-width: 1500px; }
	.page-header {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 24px;
		margin-bottom: 20px;
	}
	.page-header h1 { margin: 2px 0 5px; }
	.eyebrow {
		margin: 0;
		color: var(--accent);
		font-size: 0.72rem;
		font-weight: 700;
		letter-spacing: 0.12em;
		text-transform: uppercase;
	}
	.muted { margin: 0; color: var(--text-muted); }
	button, input, select { font: inherit; }
	button { cursor: pointer; }
	.refresh, .clear, .inspect, .load-more, .close {
		border: 1px solid var(--border);
		background: var(--surface);
		color: var(--text);
		border-radius: 7px;
	}
	.refresh { padding: 8px 13px; }
	.filters {
		display: grid;
		grid-template-columns: repeat(5, minmax(120px, 1fr));
		gap: 10px;
		padding: 14px;
		margin-bottom: 18px;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--surface);
	}
	.filters label { display: grid; gap: 5px; }
	.filters label span {
		color: var(--text-muted);
		font-size: 0.72rem;
		font-weight: 650;
		letter-spacing: 0.04em;
		text-transform: uppercase;
	}
	.filters input, .filters select {
		width: 100%;
		min-width: 0;
		padding: 8px 9px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--bg);
		color: var(--text);
		box-sizing: border-box;
	}
	.conversation-filter { grid-column: span 2; }
	.filter-actions {
		display: flex;
		align-items: flex-end;
		gap: 8px;
	}
	.apply {
		padding: 9px 13px;
		border: 0;
		border-radius: 7px;
		background: var(--accent);
		color: white;
	}
	.clear { padding: 8px 12px; }
	.error {
		margin-bottom: 12px;
		padding: 10px 12px;
		border-left: 3px solid var(--danger);
		background: color-mix(in srgb, var(--danger) 8%, transparent);
		color: var(--danger);
	}
	.trace-layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 14px;
	}
	.trace-layout.with-detail { grid-template-columns: minmax(0, 1.65fr) minmax(310px, 0.85fr); }
	.trace-list, .detail {
		min-width: 0;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--surface);
	}
	.table-wrap { overflow-x: auto; }
	table { width: 100%; border-collapse: collapse; }
	th, td {
		padding: 10px 11px;
		border-bottom: 1px solid var(--border);
		text-align: left;
		vertical-align: middle;
		white-space: nowrap;
	}
	th {
		color: var(--text-muted);
		font-size: 0.69rem;
		letter-spacing: 0.055em;
		text-transform: uppercase;
	}
	tbody tr { box-shadow: inset 3px 0 transparent; }
	tbody tr.selected { background: color-mix(in srgb, var(--accent) 7%, transparent); box-shadow: inset 3px 0 var(--accent); }
	td strong { display: block; font-size: 0.88rem; }
	.timestamp, .model, .source { color: var(--text-muted); font-size: 0.78rem; }
	.source-row { display: block; }
	.source { display: block; margin-top: 2px; text-transform: uppercase; letter-spacing: 0.05em; }
	.mobile-label, .mobile-actions { display: none; }
	.status {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		padding: 3px 7px;
		border-radius: 99px;
		font-size: 0.7rem;
		font-weight: 700;
		text-transform: uppercase;
	}
	.status::before { content: ''; width: 6px; height: 6px; border-radius: 50%; background: currentColor; }
	.status.succeeded { color: var(--accent-hover); background: color-mix(in srgb, var(--accent) 10%, transparent); }
	.status.failed { color: var(--danger); background: color-mix(in srgb, var(--danger) 10%, transparent); }
	.status.running { color: var(--accent); background: color-mix(in srgb, var(--accent) 10%, transparent); }
	.inspect { padding: 5px 9px; font-size: 0.78rem; }
	.load-more { display: block; margin: 12px auto; padding: 8px 14px; }
	.state { padding: 24px; margin: 0; color: var(--text-muted); text-align: center; }
	.detail {
		position: sticky;
		top: 16px;
		align-self: start;
		max-height: calc(100vh - 32px);
		overflow: auto;
		padding: 16px;
	}
	.detail-head {
		display: flex;
		align-items: flex-start;
		justify-content: space-between;
		gap: 12px;
		padding-bottom: 13px;
		border-bottom: 1px solid var(--border);
	}
	.detail h2 { margin: 9px 0 0; font-size: 1.05rem; overflow-wrap: anywhere; }
	.close { padding: 1px 8px 3px; font-size: 1.3rem; line-height: 1; }
	.metadata { display: grid; gap: 0; margin: 10px 0 16px; }
	.metadata div {
		display: grid;
		grid-template-columns: 92px minmax(0, 1fr);
		gap: 10px;
		padding: 7px 0;
		border-bottom: 1px solid var(--border);
	}
	dt { color: var(--text-muted); font-size: 0.73rem; text-transform: uppercase; }
	dd { margin: 0; font-size: 0.8rem; overflow-wrap: anywhere; }
	.payload { margin-top: 13px; }
	.payload h3 { margin: 0 0 6px; color: var(--text-muted); font-size: 0.73rem; letter-spacing: 0.06em; text-transform: uppercase; }
	pre {
		margin: 0;
		padding: 11px;
		overflow: auto;
		border-radius: 7px;
		background: var(--bg);
		color: var(--text);
		font: 0.75rem/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.failure pre { border-left: 3px solid var(--danger); color: var(--danger); }
	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		margin: -1px;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
		border: 0;
	}
	@media (max-width: 1100px) {
		.filters { grid-template-columns: repeat(3, minmax(130px, 1fr)); }
		.trace-layout.with-detail { grid-template-columns: 1fr; }
		.detail { position: static; max-height: none; grid-row: 1; }
	}
	@media (max-width: 700px) {
		.page-header { align-items: stretch; flex-direction: column; gap: 12px; }
		.refresh { align-self: flex-start; }
		.filters { grid-template-columns: 1fr; }
		.conversation-filter { grid-column: auto; }
		.filter-actions { align-items: center; }
		.table-wrap { overflow: visible; }
		table, tbody { display: block; width: 100%; min-width: 0; }
		thead {
			position: absolute;
			width: 1px;
			height: 1px;
			padding: 0;
			margin: -1px;
			overflow: hidden;
			clip: rect(0, 0, 0, 0);
			white-space: nowrap;
			border: 0;
		}
		tbody {
			display: grid;
			gap: 10px;
			padding: 10px;
		}
		tbody tr {
			position: relative;
			display: block;
			min-width: 0;
			padding: 12px 48px 12px 14px;
			border: 1px solid var(--border);
			border-radius: calc(var(--radius) + 2px);
			box-shadow: inset 3px 0 transparent;
		}
		tbody tr.selected {
			border-color: color-mix(in srgb, var(--accent) 30%, var(--border));
			box-shadow: inset 3px 0 var(--accent);
		}
		td {
			display: grid;
			grid-template-columns: 76px minmax(0, 1fr);
			gap: 10px;
			min-width: 0;
			padding: 4px 0;
			border: 0;
			white-space: normal;
			overflow-wrap: anywhere;
		}
		.mobile-label {
			display: block;
			color: var(--text-muted);
			font-size: 0.69rem;
			font-weight: 650;
			letter-spacing: 0.04em;
			text-transform: uppercase;
		}
		.tool-value { min-width: 0; }
		.source-row {
			display: grid;
			grid-template-columns: 76px minmax(0, 1fr);
			gap: 10px;
			margin-top: 5px;
		}
		.source { margin: 0; overflow-wrap: anywhere; }
		.actions-cell {
			position: absolute;
			top: 7px;
			right: 7px;
			display: block;
			width: auto;
			padding: 0;
		}
		.inspect { display: none; }
		.mobile-actions { display: block; }
	}
</style>
