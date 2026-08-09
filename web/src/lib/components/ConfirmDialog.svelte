<script lang="ts">
	import Modal from '$lib/components/Modal.svelte';
	import Button from '$lib/components/Button.svelte';

	let {
		open = false,
		title,
		message,
		confirmLabel = 'Delete',
		cancelLabel = 'Cancel',
		onConfirm,
		onCancel
	}: {
		open?: boolean;
		title: string;
		message: string;
		confirmLabel?: string;
		cancelLabel?: string;
		onConfirm: () => void;
		onCancel: () => void;
	} = $props();

	function confirm(event: SubmitEvent): void {
		event.preventDefault();
		onConfirm();
	}
</script>

<Modal {open} {title} onClose={onCancel}>
	<form onsubmit={confirm}>
		<p class="message">{message}</p>
		<div class="actions">
			<Button type="button" variant="ghost" onclick={onCancel}>{cancelLabel}</Button>
			<Button type="submit" variant="danger" autofocus>{confirmLabel}</Button>
		</div>
	</form>
</Modal>

<style>
	.message {
		margin: 0 0 16px;
	}
	.actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}
</style>
