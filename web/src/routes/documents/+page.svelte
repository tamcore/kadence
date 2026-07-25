<script lang="ts">
	import { onMount } from 'svelte';
	import {
		listDocuments,
		deleteDocument,
		getDocumentUploadCapabilities,
		type DocumentUploadCapabilities
	} from '$lib/api/documents';
	import type { Document } from '$lib/types';
	import DocumentUpload from '$lib/components/DocumentUpload.svelte';
	import DocumentList from '$lib/components/DocumentList.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';

	let documents = $state<Document[]>([]);
	let error = $state('');
	let loading = $state(true);
	let deleteTarget = $state<Document | null>(null);
	let capabilities = $state<DocumentUploadCapabilities | null>(null);

	async function loadDocuments() {
		loading = true;
		error = '';
		try {
			documents = await listDocuments();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load documents';
		} finally {
			loading = false;
		}
	}

	async function initialize() {
		loading = true;
		error = '';
		try {
			const [loadedDocuments, loadedCapabilities] = await Promise.all([
				listDocuments(),
				getDocumentUploadCapabilities()
			]);
			documents = loadedDocuments;
			capabilities = loadedCapabilities;
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not load documents';
		} finally {
			loading = false;
		}
	}

	async function handleDelete(id: number) {
		try {
			await deleteDocument(id);
			await loadDocuments();
		} catch (e) {
			error = e instanceof Error ? e.message : 'Could not delete document';
		}
	}

	function requestDelete(id: number): void {
		deleteTarget = documents.find((d) => d.id === id) ?? null;
	}

	async function confirmDelete(): Promise<void> {
		const target = deleteTarget;
		deleteTarget = null;
		if (target) await handleDelete(target.id);
	}

	onMount(initialize);
</script>

<div class="page">
	<h1>My documents</h1>
	<p class="muted">Add files to your personal knowledge base. Their content enriches your chats.</p>
	{#if error}<div class="error" role="alert">{error}</div>{/if}

	{#if capabilities}
		<DocumentUpload {capabilities} onUploaded={loadDocuments} />
	{/if}

	{#if loading}
		<p class="muted">Loading…</p>
	{:else}
		<DocumentList {documents} ondelete={requestDelete} />
	{/if}
</div>

<ConfirmDialog
	open={deleteTarget !== null}
	title="Delete document"
	message={`Delete "${deleteTarget?.filename}"? This cannot be undone.`}
	onConfirm={confirmDelete}
	onCancel={() => (deleteTarget = null)}
/>

<style>
	.muted { color: var(--text-muted); margin-bottom: 16px; }
	.error { color: var(--danger); margin-bottom: 12px; }
</style>
