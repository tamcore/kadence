<script lang="ts">
	import type { ChatAttachment, ChatDocumentReference } from '$lib/types';

	let {
		conversationId,
		messageId,
		attachments = [],
		documentReferences = []
	}: {
		conversationId: string | null;
		messageId?: number;
		attachments?: ChatAttachment[];
		documentReferences?: ChatDocumentReference[];
	} = $props();

	function attachmentPath(attachment: ChatAttachment): string | null {
		if (!conversationId || messageId === undefined || attachment.id === undefined) return null;
		return `/api/conversations/${encodeURIComponent(conversationId)}/messages/${messageId}/attachments/${attachment.id}`;
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return `${bytes} B`;
		if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
		return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
	}

	function referenceLabel(reference: ChatDocumentReference): string {
		const scope = reference.scope === 'public' ? 'Public reference' : 'Private reference';
		return reference.available ? scope : `${scope} · unavailable`;
	}
</script>

{#if attachments.length > 0 || documentReferences.length > 0}
	<div class="message-resources">
		{#if attachments.length > 0}
			<div class="attachments" aria-label="Message attachments">
				{#each attachments as attachment, index (`${attachment.id ?? 'pending'}:${attachment.ordinal}:${index}`)}
					{@const path = attachmentPath(attachment)}
					{#if attachment.kind === 'image' && path}
						<a
							class="image-attachment"
							href={path}
							target="_blank"
							rel="noreferrer"
							aria-label={`Open ${attachment.filename}`}
						>
							<img src={path} alt={attachment.filename} loading="lazy" />
							<span>{attachment.filename}</span>
						</a>
					{:else if path}
						<a
							class="file-attachment"
							href={path}
							download={attachment.filename}
							aria-label={`Download ${attachment.filename}`}
						>
							<span class="file-mark" aria-hidden="true">▤</span>
							<span class="file-copy">
								<strong>{attachment.filename}</strong>
								<small>{formatSize(attachment.sizeBytes)}</small>
							</span>
							<span aria-hidden="true">↓</span>
						</a>
					{:else}
						<div class="file-attachment pending">
							<span class="file-mark" aria-hidden="true">
								{attachment.kind === 'image' ? '▧' : '▤'}
							</span>
							<span class="file-copy">
								<strong>{attachment.filename}</strong>
								<small>Sending · {formatSize(attachment.sizeBytes)}</small>
							</span>
						</div>
					{/if}
				{/each}
			</div>
		{/if}

		{#if documentReferences.length > 0}
			<ul class="references" aria-label="Referenced documents">
				{#each documentReferences as reference, index (`${reference.id ?? 'pending'}:${reference.ordinal}:${index}`)}
					<li class:unavailable={!reference.available}>
						<span class="reference-mark" aria-hidden="true">@</span>
						<span class="reference-copy">
							<strong>{reference.filename}</strong>
							<small>{referenceLabel(reference)}</small>
						</span>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
{/if}

<style>
	.message-resources {
		display: grid;
		gap: 8px;
		margin-bottom: 8px;
	}

	.attachments {
		display: flex;
		flex-wrap: wrap;
		gap: 7px;
	}

	.image-attachment {
		position: relative;
		display: block;
		width: min(220px, 100%);
		overflow: hidden;
		border: 1px solid color-mix(in srgb, var(--on-accent) 42%, transparent);
		border-radius: 7px;
		background: color-mix(in srgb, var(--on-accent) 12%, transparent);
		color: var(--on-accent);
		text-decoration: none;
	}

	.image-attachment img {
		display: block;
		width: 100%;
		max-height: 180px;
		object-fit: cover;
		background: color-mix(in srgb, var(--text) 8%, transparent);
	}

	.image-attachment > span {
		display: block;
		overflow: hidden;
		padding: 5px 7px;
		font-size: 0.75rem;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.file-attachment {
		display: grid;
		min-width: min(220px, 100%);
		max-width: 300px;
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: 8px;
		align-items: center;
		padding: 7px 9px;
		border: 1px solid color-mix(in srgb, var(--on-accent) 42%, transparent);
		border-radius: 7px;
		background: color-mix(in srgb, var(--on-accent) 12%, transparent);
		color: var(--on-accent);
		text-decoration: none;
	}

	.file-attachment.pending {
		grid-template-columns: auto minmax(0, 1fr);
	}

	.file-mark,
	.reference-mark {
		font-weight: 700;
	}

	.file-copy,
	.reference-copy {
		display: flex;
		min-width: 0;
		flex-direction: column;
		line-height: 1.2;
	}

	.file-copy strong,
	.reference-copy strong {
		overflow: hidden;
		font-size: 0.79rem;
		text-overflow: ellipsis;
		white-space: nowrap;
	}

	.file-copy small,
	.reference-copy small {
		opacity: 0.78;
		font-size: 0.69rem;
	}

	.references {
		display: flex;
		flex-wrap: wrap;
		gap: 6px;
		margin: 0;
		padding: 0;
		list-style: none;
	}

	.references li {
		display: grid;
		max-width: 280px;
		grid-template-columns: auto minmax(0, 1fr);
		gap: 7px;
		align-items: center;
		padding: 6px 9px;
		border: 1px solid color-mix(in srgb, var(--on-accent) 42%, transparent);
		border-radius: 7px;
		background: color-mix(in srgb, var(--on-accent) 12%, transparent);
	}

	.references li.unavailable {
		border-style: dashed;
		opacity: 0.75;
	}

	a:hover {
		background: color-mix(in srgb, var(--on-accent) 20%, transparent);
	}

	a:focus-visible {
		outline: 2px solid var(--on-accent);
		outline-offset: 2px;
	}
</style>
