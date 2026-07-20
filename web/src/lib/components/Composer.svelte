<script lang="ts">
	import Button from '$lib/components/Button.svelte';

	const TEXTAREA_MAX_HEIGHT_PX = 200;

	interface Props {
		placeholder?: string;
		disabled?: boolean;
		onSubmit: (text: string) => void;
	}

	let { placeholder = 'Ask your coach…', disabled = false, onSubmit }: Props = $props();
	let text = $state('');

	function submit(): void {
		const trimmed = text.trim();
		if (!trimmed || disabled) return;
		text = '';
		onSubmit(trimmed);
	}

	function handleFormSubmit(e: Event): void {
		e.preventDefault();
		submit();
	}

	function handleKeydown(e: KeyboardEvent): void {
		if (e.key !== 'Enter' || e.shiftKey) return;
		e.preventDefault();
		submit();
	}
</script>

<form class="composer" onsubmit={handleFormSubmit}>
	<textarea
		bind:value={text}
		{placeholder}
		{disabled}
		rows="1"
		aria-label="Message"
		style:max-height="{TEXTAREA_MAX_HEIGHT_PX}px"
		onkeydown={handleKeydown}
	></textarea>
	<Button type="submit" variant="primary" {disabled}>Send</Button>
</form>

<style>
	.composer {
		display: flex;
		gap: 8px;
		align-items: flex-end;
		border-top: 1px solid var(--border);
		padding-top: 12px;
	}
	textarea {
		flex: 1;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
		resize: none;
		overflow-y: auto;
	}
</style>
