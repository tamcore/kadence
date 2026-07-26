<script lang="ts">
	import { onMount } from 'svelte';

	let {
		file,
		onRemove
	}: {
		file: File;
		onRemove: () => void;
	} = $props();

	let previewURL = $state('');

	onMount(() => {
		if (
			!file.type.startsWith('image/') ||
			typeof URL === 'undefined' ||
			typeof URL.createObjectURL !== 'function'
		) {
			return;
		}
		previewURL = URL.createObjectURL(file);
		return () => {
			URL.revokeObjectURL(previewURL);
		};
	});

	function formatSize(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}
</script>

<li>
	{#if previewURL}
		<img src={previewURL} alt={`Preview ${file.name}`} />
	{:else}
		<span class="kind" aria-hidden="true">{file.type.startsWith('image/') ? '▧' : '▤'}</span>
	{/if}
	<span class="file-copy">
		<strong title={file.name}>{file.name}</strong>
		<small>{formatSize(file.size)}</small>
	</span>
	<button type="button" aria-label={`Remove ${file.name}`} onclick={onRemove}>×</button>
</li>

<style>
	li {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: 7px;
		align-items: center;
		max-width: min(280px, 100%);
		padding: 6px 7px 6px 9px;
		border: 1px solid var(--border);
		border-radius: 7px;
		background: var(--surface);
	}

	img {
		width: 30px;
		height: 30px;
		border-radius: 5px;
		object-fit: cover;
		background: var(--bg);
	}

	.kind {
		color: var(--accent);
		font-size: 0.95rem;
	}

	.file-copy {
		display: flex;
		min-width: 0;
		flex-direction: column;
		line-height: 1.15;
	}

	strong {
		overflow: hidden;
		font-size: 0.82rem;
		font-weight: 600;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	small {
		color: var(--text-muted);
		font-size: 0.7rem;
	}

	button {
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

	button:hover {
		background: var(--bg);
		color: var(--danger);
	}

	button:focus-visible {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}
</style>
