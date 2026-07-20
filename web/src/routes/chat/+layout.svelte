<script lang="ts">
	// History is loaded by the global Sidebar (shell); this layout just renders
	// the chat pages into the shell's main pane.
	let { children } = $props();
</script>

{@render children()}
