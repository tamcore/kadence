<script lang="ts">
	import { uploadDocument } from '$lib/api/documents';
	import { APIError } from '$lib/api/client';
	import Button from '$lib/components/Button.svelte';

	let { admin = false, onUploaded }: { admin?: boolean; onUploaded: () => void } = $props();

	let files = $state<FileList | null>(null);
	let uploading = $state(false);
	let error = $state('');

	function messageFor(err: unknown): string {
		if (err instanceof APIError) {
			if (err.status === 415) return 'Only PDF files are supported.';
			if (err.status === 413) return 'File is too large.';
		}
		return 'Upload failed. Please try again.';
	}

	async function handleUpload() {
		const file = files?.[0];
		if (!file) {
			error = 'Choose a file first.';
			return;
		}
		uploading = true;
		error = '';
		try {
			await uploadDocument(file, { admin });
			files = null;
			onUploaded();
		} catch (err) {
			error = messageFor(err);
		} finally {
			uploading = false;
		}
	}
</script>

<div class="upload">
	<input type="file" accept=".pdf,application/pdf" onchange={(e) => (files = (e.currentTarget as HTMLInputElement).files)} />
	<Button onclick={handleUpload} loading={uploading}>Upload</Button>
</div>
{#if error}<div class="error" role="alert">{error}</div>{/if}

<style>
	.upload { display: flex; align-items: center; gap: 12px; margin-bottom: 12px; }
	.error { color: var(--danger); margin-bottom: 12px; }
</style>
