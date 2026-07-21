<script lang="ts">
	import Button from '$lib/components/Button.svelte';
	import { tick } from 'svelte';

	const TEXTAREA_MAX_HEIGHT_PX = 200;

	interface Props {
		placeholder?: string;
		disabled?: boolean;
		onSubmit: (text: string) => void;
	}

	let { placeholder = 'Ask your coach…', disabled = false, onSubmit }: Props = $props();
	let text = $state('');
	let textareaEl: HTMLTextAreaElement | undefined;

	function autosize(): void {
		if (!textareaEl) return;
		textareaEl.style.height = 'auto';
		textareaEl.style.height = `${Math.min(textareaEl.scrollHeight, TEXTAREA_MAX_HEIGHT_PX)}px`;
	}

	function submit(): void {
		const trimmed = text.trim();
		if (!trimmed || disabled) return;
		text = '';
		void tick().then(autosize);
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
		bind:this={textareaEl}
		bind:value={text}
		{placeholder}
		{disabled}
		rows="1"
		aria-label="Message"
		style:max-height="{TEXTAREA_MAX_HEIGHT_PX}px"
		oninput={autosize}
		onkeydown={handleKeydown}
	></textarea>
	<Button type="submit" variant="primary" {disabled}>Send</Button>
</form>

<style>
	.composer {
		display: flex;
		gap: 8px;
		align-items: flex-end;
	}
	textarea {
		flex: 1;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		font: inherit;
		resize: none;
		overflow-y: auto;
		box-sizing: border-box;
	}
</style>
