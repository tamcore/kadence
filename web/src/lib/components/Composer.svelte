<script lang="ts">
	import Button from '$lib/components/Button.svelte';
	import ChatAttachmentTray from '$lib/components/ChatAttachmentTray.svelte';
	import DocumentReferencePicker from '$lib/components/DocumentReferencePicker.svelte';
	import { getDocumentUploadCapabilities } from '$lib/api/documents';
	import type { Document } from '$lib/types';
	import { onMount, tick } from 'svelte';

	const TEXTAREA_MAX_HEIGHT_PX = 200;
	const MAX_FILES = 5;
	const DEFAULT_MAX_BYTES = 10 * 1024 * 1024;
	const IMAGE_ACCEPT = ['image/png', 'image/jpeg', 'image/webp', 'image/gif'];

	interface Props {
		placeholder?: string;
		disabled?: boolean;
		richInput?: boolean;
		onSubmit: (
			text: string,
			files?: File[],
			documentReferences?: Document[]
		) => void | boolean | Promise<void | boolean>;
	}

	let {
		placeholder = 'Ask your coach…',
		disabled = false,
		richInput = false,
		onSubmit
	}: Props = $props();
	let text = $state('');
	let files = $state<File[]>([]);
	let documentReferences = $state<Document[]>([]);
	let maxBytes = $state(DEFAULT_MAX_BYTES);
	let accept = $state(IMAGE_ACCEPT.join(','));
	let validationError = $state('');
	let dragDepth = $state(0);
	let dragActive = $state(false);
	let referencePickerVersion = $state(0);
	let textareaEl: HTMLTextAreaElement | undefined;

	const canSubmit = $derived(
		!disabled &&
			(text.trim().length > 0 ||
				(richInput && (files.length > 0 || documentReferences.length > 0)))
	);

	onMount(() => {
		if (!richInput) return;
		void getDocumentUploadCapabilities()
			.then((capabilities) => {
				maxBytes = capabilities.max_bytes;
				accept = Array.from(
					new Set([
						...IMAGE_ACCEPT,
						...capabilities.accept.split(',').map((value) => value.trim()).filter(Boolean)
					])
				).join(',');
			})
			.catch(() => {
				// Backend remains final authority; image attachments still work.
			});
	});

	function autosize(): void {
		if (!textareaEl) return;
		textareaEl.style.height = 'auto';
		textareaEl.style.height = `${Math.min(textareaEl.scrollHeight, TEXTAREA_MAX_HEIGHT_PX)}px`;
	}

	async function submit(): Promise<void> {
		const submittedText = text;
		const trimmed = text.trim();
		if (!canSubmit) return;
		const submittedFiles = files;
		const submittedReferences = documentReferences;
		text = '';
		files = [];
		documentReferences = [];
		referencePickerVersion += 1;
		validationError = '';
		void tick().then(autosize);
		try {
			const accepted =
				submittedFiles.length === 0 && submittedReferences.length === 0
					? await onSubmit(trimmed)
					: await onSubmit(trimmed, submittedFiles, submittedReferences);
			if (accepted !== false) return;
		} catch {
			// A rejected request has not persisted the turn and remains retryable.
		}
		text = submittedText;
		files = submittedFiles;
		documentReferences = submittedReferences;
		void tick().then(autosize);
	}

	function handleFormSubmit(e: Event): void {
		e.preventDefault();
		void submit();
	}

	function handleKeydown(e: KeyboardEvent): void {
		if (e.key !== 'Enter' || e.shiftKey) return;
		e.preventDefault();
		void submit();
	}

	function addFiles(incomingFiles: FileList | File[]): void {
		if (!richInput || disabled) return;
		validationError = '';
		const incoming = Array.from(incomingFiles);
		if (incoming.length === 0) return;
		const remaining = Math.max(0, MAX_FILES - files.length);
		const accepted = incoming.slice(0, remaining);
		if (incoming.length > remaining) {
			validationError = `You can attach up to ${MAX_FILES} files.`;
		}
		const candidate = [...files, ...accepted];
		const totalBytes = candidate.reduce((total, file) => total + file.size, 0);
		if (totalBytes > maxBytes) {
			const megabytes = Math.max(1, Math.round(maxBytes / (1024 * 1024)));
			validationError = `Attachments exceed the ${megabytes} MB total limit.`;
			return;
		}
		files = candidate;
	}

	function handleFileSelection(event: Event): void {
		const input = event.currentTarget as HTMLInputElement;
		if (input.files) addFiles(input.files);
		input.value = '';
	}

	function removeFile(index: number): void {
		files = files.filter((_, fileIndex) => fileIndex !== index);
		validationError = '';
	}

	function containsFiles(event: DragEvent): boolean {
		return Array.from(event.dataTransfer?.types ?? []).includes('Files');
	}

	function handleDragEnter(event: DragEvent): void {
		if (!richInput) return;
		if (!containsFiles(event)) return;
		event.preventDefault();
		if (disabled) return;
		dragDepth += 1;
		dragActive = true;
	}

	function handleDragOver(event: DragEvent): void {
		if (!richInput) return;
		if (!containsFiles(event)) return;
		event.preventDefault();
		if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
	}

	function handleDragLeave(event: DragEvent): void {
		if (!richInput) return;
		if (!dragActive && dragDepth === 0) return;
		event.preventDefault();
		dragDepth = Math.max(0, dragDepth - 1);
		if (dragDepth === 0) dragActive = false;
	}

	function handleDrop(event: DragEvent): void {
		if (!richInput) return;
		if (!containsFiles(event)) return;
		event.preventDefault();
		dragDepth = 0;
		dragActive = false;
		if (event.dataTransfer?.files) addFiles(event.dataTransfer.files);
	}

	function handlePaste(event: ClipboardEvent): void {
		if (!richInput) return;
		const images = Array.from(event.clipboardData?.files ?? []).filter((file) =>
			file.type.startsWith('image/')
		);
		if (images.length === 0) return;
		event.preventDefault();
		addFiles(images);
	}
</script>

<svelte:window
	ondragenter={handleDragEnter}
	ondragover={handleDragOver}
	ondragleave={handleDragLeave}
	ondrop={handleDrop}
/>

{#if richInput && dragActive}
	<div class="drop-overlay" role="status" aria-label="Chat file drop area">
		<div class="drop-callout">
			<span aria-hidden="true">＋</span>
			<strong>Drop files into this chat</strong>
			<small>Images and supported documents will be sent with your next message.</small>
		</div>
	</div>
{/if}

<div class="composer-shell">
	{#if richInput && (files.length > 0 || documentReferences.length > 0)}
		<div class="evidence-rail" aria-label="Items to send">
			<ChatAttachmentTray {files} onRemove={removeFile} />
			{#if documentReferences.length > 0}
				<ul class="reference-tray" aria-label="Documents to reference">
					{#each documentReferences as document (document.id)}
						<li>
							<span aria-hidden="true">@</span>
							<strong title={document.filename}>{document.filename}</strong>
							<button
								type="button"
								aria-label={`Remove ${document.filename}`}
								onclick={() =>
									(documentReferences = documentReferences.filter(
										(item) => item.id !== document.id
									))}
							>×</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}

	{#if richInput && validationError}
		<div class="validation-error" role="alert">{validationError}</div>
	{/if}

	<form class="composer" onsubmit={handleFormSubmit}>
		{#if richInput}
			<div class="composer-tools">
				<label class="attach-control" title="Attach files">
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
							<path
								d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"
							/>
						</svg>
					</span>
					<span class="sr-only">Attach files</span>
					<input
						type="file"
						multiple
						{accept}
						{disabled}
						onchange={handleFileSelection}
					/>
				</label>
				{#key referencePickerVersion}
					<DocumentReferencePicker
						selected={documentReferences}
						{disabled}
						onChange={(documents) => (documentReferences = documents)}
					/>
				{/key}
			</div>
		{/if}
		<textarea
			bind:this={textareaEl}
			bind:value={text}
			{placeholder}
			{disabled}
			rows="1"
			aria-label="Message"
			style:max-height="{TEXTAREA_MAX_HEIGHT_PX}px"
			oninput={autosize}
			onkeydown={handleKeydown}
			onpaste={handlePaste}
		></textarea>
		<Button type="submit" variant="primary" disabled={!canSubmit}>Send</Button>
	</form>
</div>

<style>
	.composer-shell {
		--composer-control-h: 42px;
		display: grid;
		width: 100%;
		gap: 8px;
	}

	.composer {
		display: flex;
		gap: 8px;
		align-items: flex-end;
	}

	.composer-tools {
		display: flex;
		gap: 6px;
		align-items: center;
	}

	.attach-control {
		display: grid;
		width: var(--composer-control-h);
		height: var(--composer-control-h);
		place-items: center;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--surface);
		color: var(--text-muted);
		cursor: pointer;
	}

	.attach-control:hover {
		border-color: var(--accent);
		color: var(--accent);
	}

	.attach-control:focus-within {
		outline: 2px solid var(--accent);
		outline-offset: 2px;
	}

	.composer-icon {
		display: grid;
		width: 20px;
		height: 20px;
		place-items: center;
		line-height: 1;
	}

	input[type='file'] {
		position: absolute;
		width: 1px;
		height: 1px;
		overflow: hidden;
		clip: rect(0, 0, 0, 0);
		white-space: nowrap;
	}

	textarea {
		flex: 1;
		min-width: 0;
		min-height: var(--composer-control-h);
		padding: 8px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
		line-height: 24px;
		resize: none;
		overflow-y: auto;
		box-sizing: border-box;
	}

	.evidence-rail {
		display: flex;
		flex-wrap: wrap;
		gap: 7px;
		padding-left: calc(var(--composer-control-h) + 6px);
	}

	.reference-tray {
		display: flex;
		flex-wrap: wrap;
		gap: 7px;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.reference-tray li {
		display: grid;
		max-width: min(280px, 100%);
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: 7px;
		align-items: center;
		padding: 6px 7px 6px 9px;
		border: 1px solid color-mix(in srgb, var(--accent) 38%, var(--border));
		border-radius: 7px;
		background: color-mix(in srgb, var(--accent) 7%, var(--surface));
		color: var(--accent);
	}

	.reference-tray strong {
		overflow: hidden;
		color: var(--text);
		font-size: 0.82rem;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.reference-tray button {
		display: grid;
		width: 24px;
		height: 24px;
		padding: 0;
		place-items: center;
		border: 0;
		border-radius: 50%;
		background: transparent;
		color: var(--text-muted);
		cursor: pointer;
		font: inherit;
		font-size: 1.05rem;
	}

	.validation-error {
		padding-left: calc(var(--composer-control-h) + 6px);
		color: var(--danger);
		font-size: 0.8rem;
	}

	.drop-overlay {
		position: fixed;
		z-index: 80;
		inset: 0;
		display: grid;
		padding: 24px;
		place-items: center;
		background: color-mix(in srgb, var(--accent) 15%, rgba(247, 248, 250, 0.92));
		pointer-events: none;
		animation: veil-in 120ms ease-out;
	}

	.drop-callout {
		display: grid;
		width: min(440px, 100%);
		gap: 5px;
		justify-items: center;
		padding: 34px 24px;
		border: 2px dashed var(--accent);
		border-radius: 14px;
		background: var(--surface);
		box-shadow: var(--shadow);
		text-align: center;
	}

	.drop-callout > span {
		display: grid;
		width: 38px;
		height: 38px;
		margin-bottom: 4px;
		place-items: center;
		border-radius: 50%;
		background: var(--accent);
		color: white;
		font-size: 1.3rem;
	}

	.drop-callout small {
		color: var(--text-muted);
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

	@keyframes veil-in {
		from { opacity: 0; }
		to { opacity: 1; }
	}

	@media (max-width: 520px) {
		.composer {
			display: grid;
			grid-template-columns: auto minmax(0, 1fr) auto;
		}

		.evidence-rail,
		.validation-error {
			padding-left: 0;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.drop-overlay {
			animation: none;
		}
	}
</style>
