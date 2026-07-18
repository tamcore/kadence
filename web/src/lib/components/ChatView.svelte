<script lang="ts">
	import { activeId, chatError, messages, sendMessage, sending } from '$lib/stores/chat';
	import MarkdownMessage from '$lib/components/MarkdownMessage.svelte';
	import Button from '$lib/components/Button.svelte';

	let { onNewConversation }: { onNewConversation?: (id: number) => void } = $props();
	let draft = $state('');

	async function submit(e?: Event) {
		e?.preventDefault();
		const text = draft.trim();
		if (!text || $sending) return;
		draft = '';
		const wasNew = $activeId === null;
		const id = await sendMessage(text);
		if (wasNew && id != null && onNewConversation) onNewConversation(id);
	}
</script>

<div class="chat">
	<div class="thread">
		{#each $messages as m, i (i)}
			<div class="msg {m.role}">
				{#if m.role === 'assistant'}
					<MarkdownMessage content={m.content} />
				{:else}
					<p>{m.content}</p>
				{/if}
			</div>
		{/each}
		{#if $chatError}<div class="error" role="alert">{$chatError}</div>{/if}
	</div>

	<form class="composer" onsubmit={submit}>
		<textarea
			bind:value={draft}
			placeholder="Ask your coach…"
			rows="2"
			aria-label="Message"
			onkeydown={(e) => {
				if (e.key === 'Enter' && !e.shiftKey) {
					e.preventDefault();
					void submit();
				}
			}}
		></textarea>
		<Button type="submit" variant="primary" loading={$sending}>Send</Button>
	</form>
</div>

<style>
	.chat { display: flex; flex-direction: column; height: 100%; }
	.thread { flex: 1; overflow-y: auto; display: flex; flex-direction: column; gap: 16px; padding-bottom: 16px; }
	.msg { max-width: 80%; padding: 10px 14px; border-radius: var(--radius); }
	.msg.user { align-self: flex-end; background: var(--accent); color: #fff; }
	.msg.assistant { align-self: flex-start; background: var(--surface); border: 1px solid var(--border); }
	.msg p { margin: 0; }
	.error { color: var(--danger); }
	.composer { display: flex; gap: 8px; align-items: flex-end; border-top: 1px solid var(--border); padding-top: 12px; }
	textarea { flex: 1; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius); font: inherit; resize: vertical; }
</style>
