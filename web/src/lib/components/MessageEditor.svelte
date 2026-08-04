<script lang="ts">
	import { tick } from 'svelte';
	import Button from '$lib/components/Button.svelte';
	import { canReachServer } from '$lib/stores/connection';

	const TEXTAREA_MAX_HEIGHT_PX = 200;

	let {
		initialText,
		disabled = false,
		onSave,
		onCancel
	}: {
		initialText: string;
		disabled?: boolean;
		onSave: (text: string) => void;
		onCancel: () => void;
	} = $props();

	let text = $state('');
	let textareaEl: HTMLTextAreaElement | undefined;

	function initialize(node: HTMLTextAreaElement): { destroy: () => void } {
		textareaEl = node;
		text = initialText;
		void tick().then(() => {
			autosize();
			node.focus();
			node.setSelectionRange(text.length, text.length);
		});
		return {
			destroy: () => {
				if (textareaEl === node) textareaEl = undefined;
			}
		};
	}

	function autosize(): void {
		if (!textareaEl) return;
		textareaEl.style.height = 'auto';
		textareaEl.style.height = `${Math.min(textareaEl.scrollHeight, TEXTAREA_MAX_HEIGHT_PX)}px`;
	}

	function save(): void {
		const trimmed = text.trim();
		if (!trimmed || disabled) return;
		onSave(trimmed);
	}

	function submit(event: SubmitEvent): void {
		event.preventDefault();
		save();
	}

	function keydown(event: KeyboardEvent): void {
		if (event.key === 'Escape') {
			event.preventDefault();
			onCancel();
			return;
		}
		if (event.key === 'Enter' && !event.shiftKey) {
			event.preventDefault();
			save();
		}
	}
</script>

<form class="message-editor" onsubmit={submit}>
	<textarea
		use:initialize
		bind:value={text}
		rows="1"
		aria-label="Edit message"
		{disabled}
		style:max-height="{TEXTAREA_MAX_HEIGHT_PX}px"
		oninput={autosize}
		onkeydown={keydown}
	></textarea>
	<div class="editor-actions">
		<Button variant="ghost" onclick={onCancel} {disabled}>Cancel</Button>
		<Button type="submit" disabled={disabled || !$canReachServer}>Save edit</Button>
	</div>
</form>

<style>
	.message-editor {
		width: min(520px, 70vw);
		display: flex;
		flex-direction: column;
		gap: 8px;
	}
	textarea {
		width: 100%;
		min-height: 42px;
		padding: 10px 12px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--text);
		font: inherit;
		resize: none;
		overflow-y: auto;
		box-sizing: border-box;
	}
	textarea:focus {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}
	.editor-actions {
		display: flex;
		justify-content: flex-end;
		gap: 8px;
	}
	.editor-actions :global(.btn) {
		padding: 7px 12px;
	}
	@media (max-width: 600px) {
		.message-editor {
			width: min(100%, calc(100vw - 68px));
		}
	}
</style>
