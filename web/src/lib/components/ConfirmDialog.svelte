<script lang="ts">
	import type { Action } from 'svelte/action';
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

	const ownInitialFocus: Action<HTMLFormElement> = (form) => {
		let returnFocus: HTMLElement | undefined;
		const timeout = window.setTimeout(() => {
			const dialog = form.closest('[role="dialog"]');
			if (dialog?.contains(document.activeElement)) return;
			if (document.activeElement instanceof HTMLElement) returnFocus = document.activeElement;
			form.querySelector<HTMLButtonElement>('button[type="submit"]')?.focus();
		});

		return {
			destroy: () => {
				window.clearTimeout(timeout);
				const dialog = form.closest('[role="dialog"]');
				if (dialog?.contains(document.activeElement) && returnFocus?.isConnected) returnFocus.focus();
			}
		};
	};
</script>

<Modal {open} {title} onClose={onCancel}>
	<form use:ownInitialFocus onsubmit={confirm}>
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
