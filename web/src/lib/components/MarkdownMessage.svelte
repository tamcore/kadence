<script lang="ts">
	import { renderMarkdown } from '$lib/markdown';
	let { content }: { content: string } = $props();
	const html = $derived(renderMarkdown(content));
</script>

<div class="md">{@html html}</div>

<style>
	.md :global(pre) { overflow-x: auto; background: #f0f2f4; padding: 12px; border-radius: var(--radius); }
	.md :global(code) { font-family: ui-monospace, monospace; }
	.md :global(.table-scroll) { overflow-x: auto; }
	.md :global(table) { border-collapse: collapse; }
	.md :global(th), .md :global(td) { border: 1px solid var(--border); padding: 6px 10px; }
	.md :global(p:first-child) { margin-top: 0; }
	.md :global(p:last-child) { margin-bottom: 0; }
</style>
