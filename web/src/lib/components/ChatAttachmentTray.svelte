<script lang="ts">
	import ChatAttachmentItem from '$lib/components/ChatAttachmentItem.svelte';

	let {
		files,
		onRemove
	}: {
		files: File[];
		onRemove: (index: number) => void;
	} = $props();

</script>

{#if files.length > 0}
	<ul class="attachment-tray" aria-label="Files to send">
		{#each files as file, index (`${file.name}:${file.size}:${file.lastModified}:${index}`)}
			<ChatAttachmentItem {file} onRemove={() => onRemove(index)} />
		{/each}
	</ul>
{/if}

<style>
	.attachment-tray {
		display: flex;
		flex-wrap: wrap;
		gap: 7px;
		margin: 0;
		padding: 0;
		list-style: none;
	}

</style>
