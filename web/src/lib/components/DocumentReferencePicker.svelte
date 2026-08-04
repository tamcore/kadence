<script lang="ts">
	import { listDocumentReferences } from '$lib/api/documents';
	import type { Document } from '$lib/types';

	const MAX_REFERENCES = 10;

	let {
		selected,
		disabled = false,
		onChange
	}: {
		selected: Document[];
		disabled?: boolean;
		onChange: (documents: Document[]) => void;
	} = $props();

	let open = $state(false);
	let loading = $state(false);
	let loaded = $state(false);
	let error = $state('');
	let query = $state('');
	let own = $state<Document[]>([]);
	let publicDocuments = $state<Document[]>([]);

	const normalizedQuery = $derived(query.trim().toLocaleLowerCase());
	const filteredOwn = $derived(
		own.filter((document) => document.filename.toLocaleLowerCase().includes(normalizedQuery))
	);
	const filteredPublic = $derived(
		publicDocuments.filter((document) =>
			document.filename.toLocaleLowerCase().includes(normalizedQuery)
		)
	);

	function selectedDocument(id: number): boolean {
		return selected.some((document) => document.id === id);
	}

	async function toggleOpen(): Promise<void> {
		if (disabled) return;
		open = !open;
		if (!open || loaded || loading) return;
		loading = true;
		error = '';
		try {
			const options = await listDocumentReferences();
			own = options.own;
			publicDocuments = options.public;
			loaded = true;
		} catch {
			error = 'Could not load documents.';
		} finally {
			loading = false;
		}
	}

	function toggleDocument(document: Document): void {
		if (disabled) return;
		if (selectedDocument(document.id)) {
			onChange(selected.filter((item) => item.id !== document.id));
			return;
		}
		if (selected.length >= MAX_REFERENCES) return;
		onChange([...selected, document]);
	}

	function handleSearchKeydown(event: KeyboardEvent): void {
		if (event.key === 'Enter') event.preventDefault();
	}
</script>

<div class="reference-picker">
	<button
		class="reference-trigger"
		type="button"
		aria-label="Reference documents"
		aria-expanded={open}
		disabled={disabled}
		onclick={toggleOpen}
	>
		<span class="composer-icon">
			<svg
				width="20"
				height="20"
				viewBox="0 0 24 24"
				fill="none"
				stroke="currentColor"
				stroke-width="1.75"
				stroke-linecap="round"
				stroke-linejoin="round"
				aria-hidden="true"
				focusable="false"
			>
				<circle cx="12" cy="12" r="4" />
				<path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-3.92 7.94" />
			</svg>
		</span>
		{#if selected.length > 0}<b>{selected.length}</b>{/if}
	</button>

	{#if open}
		<section class="reference-panel" aria-label="Choose document references">
			<div class="panel-heading">
				<strong>Reference documents</strong>
				<button type="button" aria-label="Close document references" onclick={() => (open = false)}>×</button>
			</div>
			<label>
				<span class="sr-only">Search documents</span>
				<input
					type="search"
					aria-label="Search documents"
					placeholder="Search by filename"
					bind:value={query}
					disabled={disabled}
					onkeydown={handleSearchKeydown}
				/>
			</label>

			{#if loading}
				<p class="status">Loading documents…</p>
			{:else if error}
				<p class="error" role="alert">{error}</p>
			{:else}
				<div class="document-groups">
					<section>
						<h3>Your documents</h3>
						{#if filteredOwn.length === 0}
							<p class="status">No matching documents.</p>
						{:else}
							<ul>
								{#each filteredOwn as document (document.id)}
									<li>
										<button
											type="button"
											class:selected={selectedDocument(document.id)}
											aria-label={`${selectedDocument(document.id) ? 'Remove' : 'Add'} ${document.filename}`}
											disabled={disabled ||
												(!selectedDocument(document.id) && selected.length >= MAX_REFERENCES)}
											onclick={() => toggleDocument(document)}
										>
											<span>{document.filename}</span>
											<small>{selectedDocument(document.id) ? 'Selected' : 'Private'}</small>
										</button>
									</li>
								{/each}
							</ul>
						{/if}
					</section>
					<section>
						<h3>Public docs</h3>
						{#if filteredPublic.length === 0}
							<p class="status">No matching documents.</p>
						{:else}
							<ul>
								{#each filteredPublic as document (document.id)}
									<li>
										<button
											type="button"
											class:selected={selectedDocument(document.id)}
											aria-label={`${selectedDocument(document.id) ? 'Remove' : 'Add'} ${document.filename}`}
											disabled={disabled ||
												(!selectedDocument(document.id) && selected.length >= MAX_REFERENCES)}
											onclick={() => toggleDocument(document)}
										>
											<span>{document.filename}</span>
											<small>{selectedDocument(document.id) ? 'Selected' : 'Public'}</small>
										</button>
									</li>
								{/each}
							</ul>
						{/if}
					</section>
				</div>
				{#if selected.length >= MAX_REFERENCES}
					<p class="limit">Maximum 10 references selected.</p>
				{/if}
			{/if}
		</section>
	{/if}
</div>

<style>
	.reference-picker {
		position: relative;
	}

	.reference-trigger {
		position: relative;
		display: grid;
		width: 34px;
		height: 34px;
		padding: 0;
		place-items: center;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--surface);
		color: var(--text-muted);
		cursor: pointer;
		font: inherit;
	}

	.reference-trigger:hover {
		border-color: var(--accent);
		color: var(--accent);
	}

	.reference-trigger b {
		position: absolute;
		top: -7px;
		right: -7px;
		display: grid;
		min-width: 18px;
		height: 18px;
		padding: 0 4px;
		place-items: center;
		border-radius: 9px;
		background: var(--accent);
		color: white;
		font-size: 0.67rem;
	}

	.composer-icon {
		display: grid;
		width: 20px;
		height: 20px;
		place-items: center;
		line-height: 1;
	}

	.reference-panel {
		position: absolute;
		z-index: 25;
		bottom: calc(100% + 10px);
		left: 0;
		width: min(420px, calc(100vw - 32px));
		max-height: min(480px, 65vh);
		overflow-y: auto;
		padding: 14px;
		border: 1px solid var(--border);
		border-radius: 10px;
		background: var(--surface);
		box-shadow: var(--shadow);
	}

	.panel-heading {
		display: flex;
		align-items: center;
		justify-content: space-between;
		margin-bottom: 10px;
	}

	.panel-heading > button {
		width: 28px;
		height: 28px;
		border: 0;
		border-radius: 50%;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		font: inherit;
		font-size: 1.15rem;
	}

	input {
		width: 100%;
		padding: 8px 10px;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--bg);
		color: var(--text);
		font: inherit;
	}

	.document-groups {
		display: grid;
		gap: 14px;
		margin-top: 13px;
	}

	h3 {
		margin: 0 0 6px;
		color: var(--text-muted);
		font-size: 0.72rem;
		letter-spacing: 0.06em;
		text-transform: uppercase;
	}

	ul {
		display: grid;
		gap: 4px;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	li button {
		display: grid;
		width: 100%;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 10px;
		padding: 8px 9px;
		border: 1px solid transparent;
		border-radius: 6px;
		background: transparent;
		color: var(--text);
		cursor: pointer;
		font: inherit;
		text-align: left;
	}

	li button:hover {
		background: var(--bg);
	}

	li button.selected {
		border-color: color-mix(in srgb, var(--accent) 40%, var(--border));
		background: color-mix(in srgb, var(--accent) 8%, var(--surface));
	}

	li button span {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	li button small {
		color: var(--text-muted);
	}

	.status,
	.limit,
	.error {
		margin: 9px 0 0;
		color: var(--text-muted);
		font-size: 0.8rem;
	}

	.error {
		color: var(--danger);
	}

	.limit {
		color: var(--accent);
	}

	.sr-only {
		position: absolute;
		width: 1px;
		height: 1px;
		padding: 0;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
		border: 0;
	}

	button:focus-visible,
	input:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 2px;
	}

	@media (max-width: 520px) {
		.reference-panel {
			position: fixed;
			inset: auto 12px calc(84px + env(safe-area-inset-bottom, 0px));
			width: auto;
			max-height: 62vh;
		}
	}
</style>
