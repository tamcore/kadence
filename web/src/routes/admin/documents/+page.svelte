<script lang="ts">
	import { onMount } from 'svelte';
	import { get } from 'svelte/store';
	import { goto } from '$app/navigation';
	import { isAdmin } from '$lib/stores/auth';
	import {
		listDocuments,
		deleteDocument,
		getDocumentUploadCapabilities,
		type DocumentUploadCapabilities
	} from '$lib/api/documents';
	import type { Document } from '$lib/types';
	import DocumentUpload from '$lib/components/DocumentUpload.svelte';
	import DocumentList from '$lib/components/DocumentList.svelte';

	let documents = $state<Document[]>([]);
	let error = $state('');
	let loading = $state(true);
	let capabilities = $state<DocumentUploadCapabilities | null>(null);

	async function loadDocuments() {
		loading = true;
		error = '';
		try {
			documents = await listDocuments({ admin: true });
		} catch {
			error = 'Could not load documents';
		} finally {
			loading = false;
		}
	}

	async function initialize() {
		loading = true;
		error = '';
		const [documentResult, capabilityResult] = await Promise.allSettled([
			listDocuments({ admin: true }),
			getDocumentUploadCapabilities()
		]);
		if (documentResult.status === 'fulfilled') {
			documents = documentResult.value;
		} else {
			error = 'Could not load documents';
		}
		if (capabilityResult.status === 'fulfilled') {
			capabilities = capabilityResult.value;
		} else if (!error) {
			error = 'Could not load upload capabilities';
		}
		loading = false;
	}

	async function handleDelete(id: number) {
		try {
			await deleteDocument(id, { admin: true });
			await loadDocuments();
		} catch {
			error = 'Could not delete document';
		}
	}

	onMount(() => {
		if (!get(isAdmin)) {
			goto('/');
			return;
		}
		initialize();
	});
</script>

<div class="page">
	<h1>Shared knowledge base</h1>
	<p class="muted">Documents you publish here are available to every user's chats.</p>
	{#if error}<div class="error" role="alert">{error}</div>{/if}

	{#if capabilities}
		<DocumentUpload admin {capabilities} onUploaded={loadDocuments} />
	{/if}

	{#if loading}
		<p class="muted">Loading…</p>
	{:else}
		<DocumentList {documents} ondelete={handleDelete} />
	{/if}
</div>

<style>
	.muted { color: var(--text-muted); margin-bottom: 16px; }
	.error { color: var(--danger); margin-bottom: 12px; }
</style>
