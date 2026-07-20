<script lang="ts">
	import { submitCredentials } from '$lib/api/credentials';
	import { credentialRequest } from '$lib/stores/chat';
	import type { CredentialRequest } from '$lib/types';
	import Button from '$lib/components/Button.svelte';

	let { request }: { request: CredentialRequest } = $props();

	// Values live only in this component's local state and the POST body —
	// never in the messages store, localStorage, or a URL.
	let values = $state<Record<string, string>>({});
	let submitting = $state(false);
	let error = $state('');

	function setValue(name: string, value: string): void {
		values = { ...values, [name]: value };
	}

	async function handleSubmit(e: Event): Promise<void> {
		e.preventDefault();
		submitting = true;
		error = '';
		try {
			await submitCredentials(request.requestId, values);
			credentialRequest.set(null);
		} catch {
			error = 'Could not submit credentials. Please try again.';
		} finally {
			submitting = false;
		}
	}

	function handleCancel(): void {
		credentialRequest.set(null);
	}
</script>

<form class="credential-prompt" onsubmit={handleSubmit}>
	<p class="reason">{request.reason}</p>
	{#each request.fields as field (field.name)}
		<label class="field">
			<span>{field.label ?? field.name}</span>
			<input
				type={field.secret ? 'password' : 'text'}
				autocomplete="off"
				value={values[field.name] ?? ''}
				oninput={(e) => setValue(field.name, (e.currentTarget as HTMLInputElement).value)}
			/>
		</label>
	{/each}
	{#if error}<div class="error" role="alert">{error}</div>{/if}
	<div class="actions">
		<Button type="submit" variant="primary" disabled={submitting}>Submit</Button>
		<Button type="button" variant="ghost" disabled={submitting} onclick={handleCancel}>Cancel</Button>
	</div>
</form>

<style>
	.credential-prompt {
		display: flex;
		flex-direction: column;
		gap: 12px;
		padding: 16px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
	}
	.reason { margin: 0; font-weight: 600; }
	.field { display: flex; flex-direction: column; gap: 4px; font-size: 0.9rem; }
	.field input {
		padding: 8px 10px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
	}
	.error { color: var(--danger); }
	.actions { display: flex; gap: 8px; }
</style>
