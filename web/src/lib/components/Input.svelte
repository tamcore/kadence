<script lang="ts">
	import type { FullAutoFill } from 'svelte/elements';

	let {
		label,
		type = 'text',
		name,
		value = $bindable(''),
		required = false,
		autocomplete,
		maxlength
	}: {
		label: string;
		type?: string;
		name: string;
		value?: string;
		required?: boolean;
		autocomplete?: FullAutoFill | undefined;
		maxlength?: number | undefined;
	} = $props();
</script>

<label class="field">
	<span>{label}</span>
	{#if autocomplete}
		<input {type} {name} {required} {maxlength} {autocomplete} bind:value />
	{:else}
		<input {type} {name} {required} {maxlength} bind:value />
	{/if}
</label>

<style>
	.field { display: flex; flex-direction: column; gap: 4px; margin-bottom: 12px; }
	.field span { font-size: 0.85rem; color: var(--text-muted); }
	input {
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
		background: var(--surface);
	}
	input:focus { outline: 2px solid var(--accent); border-color: var(--accent); }
</style>
