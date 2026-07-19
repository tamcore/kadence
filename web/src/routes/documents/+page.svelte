<script lang="ts">
	import { onMount } from 'svelte';
	import { listDocuments, deleteDocument } from '$lib/api/documents';
	import type { Document } from '$lib/types';
	import DocumentUpload from '$lib/components/DocumentUpload.svelte';
	import DocumentList from '$lib/components/DocumentList.svelte';

	let documents = $state<Document[]>([]);
	let error = $state('');
	let loading = $state(true);

	async function load() {
		loading = true;
		error = '';
		try {
			documents = await listDocuments();
		} catch {
			error = 'Could not load documents';
		} finally {
			loading = false;
		}
	}

	async function handleDelete(id: number) {
		try {
			await deleteDocument(id);
			await load();
		} catch {
			error = 'Could not delete document';
		}
	}

	onMount(load);
</script>

<h1>My documents</h1>
<p class="muted">Upload PDFs to add them to your personal knowledge base. They enrich your chats.</p>
{#if error}<div class="error" role="alert">{error}</div>{/if}

<DocumentUpload onUploaded={load} />

{#if loading}
	<p class="muted">Loading…</p>
{:else}
	<DocumentList {documents} ondelete={handleDelete} />
{/if}

<style>
	.muted { color: var(--text-muted); margin-bottom: 16px; }
	.error { color: var(--danger); margin-bottom: 12px; }
</style>
