<script lang="ts">
	let {
		label,
		type = 'text',
		name,
		value = $bindable(''),
		required = false,
		autocomplete
	}: {
		label: string;
		type?: string;
		name: string;
		value?: string;
		required?: boolean;
		autocomplete?: string | undefined;
	} = $props();
</script>

<label class="field">
	<span>{label}</span>
	{#if autocomplete}
		<input {type} {name} {required} autocomplete={autocomplete as any} bind:value />
	{:else}
		<input {type} {name} {required} bind:value />
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
