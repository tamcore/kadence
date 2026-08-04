<script lang="ts">
	import { uploadDocument, type DocumentUploadCapabilities } from '$lib/api/documents';
	import { APIError } from '$lib/api/client';
	import Button from '$lib/components/Button.svelte';

	type UploadStatus = 'queued' | 'uploading' | 'success' | 'failure';
	type UploadItem = {
		id: number;
		file: File;
		status: UploadStatus;
		message: string;
	};

	let {
		admin = false,
		capabilities,
		onUploaded
	}: {
		admin?: boolean;
		capabilities: DocumentUploadCapabilities;
		onUploaded: () => void;
	} = $props();

	let queue = $state<UploadItem[]>([]);
	let uploading = $state(false);
	let dragDepth = $state(0);
	let dragActive = $state(false);
	let nextID = 0;

	const queuedCount = $derived(queue.filter((item) => item.status === 'queued').length);
	const uploadLabel = $derived(
		queuedCount === 1 ? 'Upload 1 file' : `Upload ${queuedCount} files`
	);
	const supportedFormats = $derived(
		capabilities.rich_extraction
			? 'PDF, images, text, web pages, office files, and e-books'
			: 'PDF files'
	);

	function messageFor(err: unknown): string {
		if (err instanceof APIError) {
			if (err.status === 415) return "This file type isn't supported.";
			if (err.status === 413) {
				const megabytes = Math.max(1, Math.round(capabilities.max_bytes / (1024 * 1024)));
				return `File exceeds the ${megabytes} MB upload limit.`;
			}
		}
		return 'Upload failed. Please try again.';
	}

	function addFiles(files: FileList | File[]): void {
		const incoming = Array.from(files);
		if (incoming.length === 0 || uploading) return;
		if (!queue.some((item) => item.status === 'queued')) {
			queue = [];
		}
		queue = [
			...queue,
			...incoming.map((file) => ({
				id: nextID++,
				file,
				status: 'queued' as const,
				message: 'Queued'
			}))
		];
	}

	function handleSelection(event: Event): void {
		const input = event.currentTarget as HTMLInputElement;
		if (input.files) addFiles(input.files);
		input.value = '';
	}

	function containsFiles(event: DragEvent): boolean {
		return Array.from(event.dataTransfer?.types ?? []).includes('Files');
	}

	function handleDragEnter(event: DragEvent): void {
		if (!containsFiles(event)) return;
		event.preventDefault();
		if (uploading) return;
		dragDepth += 1;
		dragActive = true;
	}

	function handleDragOver(event: DragEvent): void {
		if (!containsFiles(event)) return;
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
	}

	function handleDragLeave(event: DragEvent): void {
		if (!containsFiles(event) && !dragActive) return;
		event.preventDefault();
		dragDepth = Math.max(0, dragDepth - 1);
		if (dragDepth === 0) dragActive = false;
	}

	function handleDrop(event: DragEvent): void {
		if (!containsFiles(event)) return;
		event.preventDefault();
		dragDepth = 0;
		dragActive = false;
		if (event.dataTransfer?.files) addFiles(event.dataTransfer.files);
	}

	async function handleUpload(): Promise<void> {
		if (uploading || queuedCount === 0) return;
		uploading = true;
		let uploaded = 0;
		for (const item of queue) {
			if (item.status !== 'queued') continue;
			item.status = 'uploading';
			item.message = 'Uploading…';
			try {
				await uploadDocument(item.file, { admin });
				item.status = 'success';
				item.message = 'Uploaded';
				uploaded += 1;
			} catch (err) {
				item.status = 'failure';
				item.message = messageFor(err);
			}
		}
		uploading = false;
		if (uploaded > 0) {
			onUploaded();
		}
	}
</script>

<svelte:window
	ondragenter={handleDragEnter}
	ondragover={handleDragOver}
	ondragleave={handleDragLeave}
	ondrop={handleDrop}
/>

{#if dragActive}
	<div class="drop-overlay" role="status" aria-label="File drop area">
		<div class="drop-callout">
			<span class="drop-mark" aria-hidden="true">↓</span>
			<strong>Drop files to add them</strong>
			<span>They will wait in the upload queue.</span>
		</div>
	</div>
{/if}

<div class="upload">
	<div class="picker">
		<input
			type="file"
			multiple
			accept={capabilities.accept}
			disabled={uploading}
			onchange={handleSelection}
			aria-describedby="supported-formats"
		/>
		<p id="supported-formats">Supported: {supportedFormats}. Up to {Math.max(1, Math.round(capabilities.max_bytes / (1024 * 1024)))} MB each.</p>
	</div>
	<Button onclick={handleUpload} loading={uploading} disabled={queuedCount === 0}>{uploadLabel}</Button>
</div>

{#if queue.length > 0}
	<ul class="queue" aria-label="Upload queue" aria-live="polite">
		{#each queue as item (item.id)}
			<li class:failed={item.status === 'failure'} class:complete={item.status === 'success'}>
				<span class="filename" title={item.file.name}>{item.file.name}</span>
				<span class="status">{item.message}</span>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.upload {
		display: flex;
		align-items: flex-start;
		gap: 12px;
		margin-bottom: 12px;
		padding: 16px;
		border: 1px solid var(--border);
		border-left: 3px solid var(--accent);
		border-radius: var(--radius);
		background: var(--surface);
	}

	.picker {
		flex: 1;
		min-width: 0;
	}

	input[type='file'] {
		display: block;
		width: 100%;
		color: var(--text);
		font: inherit;
	}

	input[type='file']:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 3px;
		border-radius: 2px;
	}

	input[type='file']:disabled {
		opacity: 0.6;
		cursor: not-allowed;
	}

	p {
		margin: 7px 0 0;
		color: var(--text-muted);
		font-size: 0.84rem;
	}

	.queue {
		display: grid;
		gap: 6px;
		margin: 0 0 16px;
		padding: 0;
		list-style: none;
	}

	.queue li {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		gap: 16px;
		align-items: baseline;
		padding: 8px 10px;
		border: 1px solid var(--border);
		border-radius: 6px;
		background: var(--surface);
	}

	.queue li.complete {
		border-color: color-mix(in srgb, var(--accent) 38%, var(--border));
	}

	.queue li.failed {
		border-color: color-mix(in srgb, var(--danger) 42%, var(--border));
	}

	.filename {
		overflow: hidden;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.status {
		color: var(--text-muted);
		font-size: 0.86rem;
		text-align: right;
	}

	.failed .status {
		color: var(--danger);
	}

	.complete .status {
		color: var(--accent);
	}

	.drop-overlay {
		position: fixed;
		z-index: 80;
		inset: 0;
		display: grid;
		place-items: center;
		padding: 24px;
		background: color-mix(in srgb, var(--accent) 15%, color-mix(in srgb, var(--bg) 92%, transparent));
		pointer-events: none;
		animation: veil-in 120ms ease-out;
	}

	.drop-callout {
		display: grid;
		justify-items: center;
		gap: 5px;
		width: min(440px, 100%);
		padding: 36px 24px;
		border: 2px dashed var(--accent);
		border-radius: 14px;
		background: var(--surface);
		box-shadow: var(--shadow);
		color: var(--text);
		text-align: center;
	}

	.drop-callout span:last-child {
		color: var(--text-muted);
	}

	.drop-mark {
		display: grid;
		place-items: center;
		width: 38px;
		height: 38px;
		margin-bottom: 5px;
		border-radius: 50%;
		background: var(--accent);
		color: var(--on-accent);
		font-size: 1.35rem;
		font-weight: 700;
	}

	@keyframes veil-in {
		from { opacity: 0; }
		to { opacity: 1; }
	}

	@media (max-width: 600px) {
		.upload {
			align-items: stretch;
			flex-direction: column;
		}

		.queue li {
			grid-template-columns: 1fr;
			gap: 2px;
		}

		.status {
			text-align: left;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.drop-overlay {
			animation: none;
		}
	}
</style>
