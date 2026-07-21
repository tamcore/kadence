<script lang="ts">
	import type { McpInput } from '$lib/api/mcp';
	import Button from '$lib/components/Button.svelte';

	let {
		initial,
		submitLabel,
		formError = '',
		onSubmit,
		onCancel
	}: {
		initial?: Partial<McpInput>;
		submitLabel: string;
		formError?: string;
		onSubmit: (input: McpInput) => void | Promise<void>;
		onCancel?: () => void;
	} = $props();

	let name = $state(initial?.name ?? '');
	let url = $state(initial?.url ?? '');
	let transport = $state(initial?.transport ?? 'streamable-http');
	let authUser = $state(initial?.authUser ?? '');
	let authPass = $state('');
	let submitting = $state(false);

	async function handleSubmit(e: Event): Promise<void> {
		e.preventDefault();
		submitting = true;
		try {
			await onSubmit({ name, url, transport, authUser, authPass });
		} finally {
			submitting = false;
		}
	}
</script>

<form class="mcp-form" onsubmit={handleSubmit}>
	<label class="field">
		<span>Name</span>
		<input bind:value={name} placeholder="name" required />
	</label>
	<label class="field">
		<span>URL</span>
		<input bind:value={url} placeholder="https://…" required />
	</label>
	<label class="field">
		<span>Transport</span>
		<select bind:value={transport}>
			<option value="streamable-http">streamable-http</option>
			<option value="sse">sse</option>
		</select>
	</label>
	<label class="field">
		<span>Auth user (optional)</span>
		<input bind:value={authUser} placeholder="auth user (optional)" autocomplete="off" />
	</label>
	<label class="field">
		<span>Auth password{initial ? ' (leave blank to keep)' : ' (optional)'}</span>
		<input type="password" bind:value={authPass} placeholder="auth password" autocomplete="off" />
	</label>
	{#if formError}<p class="error" role="alert">{formError}</p>{/if}
	<div class="actions">
		<Button type="submit" variant="primary" disabled={submitting}>{submitLabel}</Button>
		{#if onCancel}
			<Button type="button" variant="ghost" disabled={submitting} onclick={onCancel}>Cancel</Button>
		{/if}
	</div>
</form>

<style>
	.mcp-form {
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 16px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
	}
	.field {
		display: flex;
		flex-direction: column;
		gap: 4px;
		font-size: 0.9rem;
	}
	.field input,
	.field select {
		padding: 8px 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
	}
	.error {
		color: var(--danger);
		margin: 0;
	}
	.actions {
		display: flex;
		gap: 8px;
	}
</style>
