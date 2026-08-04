<script lang="ts">
	import { renderMarkdown } from '$lib/markdown';
	let { content }: { content: string } = $props();
	const html = $derived(renderMarkdown(content));
</script>

<div class="md">{@html html}</div>

<style>
	.md :global(pre) { overflow-x: auto; background: var(--code-bg); padding: 12px; border-radius: var(--radius); }
	.md :global(code) { font-family: ui-monospace, monospace; }
	.md :global(.table-scroll) { overflow-x: auto; }
	.md :global(table) { border-collapse: collapse; }
	.md :global(th), .md :global(td) {
		border: 1px solid var(--border);
		padding: 6px 10px;
		/* Reset the char-by-char breaking inherited from .msg { overflow-wrap: anywhere }
		   so cells wrap only on whitespace instead of shredding words. */
		overflow-wrap: normal;
		word-break: normal;
	}
	.md :global(p:first-child) { margin-top: 0; }
	.md :global(p:last-child) { margin-bottom: 0; }

	/* On phones, keep every cell on one line so the table takes its natural width,
	   overflows the bubble, and the .table-scroll container scrolls horizontally. */
	@media (max-width: 899px) {
		.md :global(th), .md :global(td) { white-space: nowrap; }
	}
</style>
