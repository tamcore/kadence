<script lang="ts">
	import { activeId, chatError, messages, sendMessage, sending, toolActivity } from '$lib/stores/chat';
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

	function prettifyTool(name: string): string {
		const [server, ...rest] = name.split('__');
		const tool = rest.join('__').replace(/_/g, ' ');
		return rest.length ? `${server} · ${tool}` : name.replace(/_/g, ' ');
	}
	function statusIcon(status: string): string {
		return status === 'done' ? '✓' : status === 'error' ? '✗' : '⏳';
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
		{#if $toolActivity.length > 0}
			<div class="tools" role="status" aria-label="Tool activity">
				{#each $toolActivity as t, i (i)}
					<span class="tool-chip {t.status}">{statusIcon(t.status)} {prettifyTool(t.tool)}</span>
				{/each}
			</div>
		{/if}
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
	.tools { display: flex; flex-wrap: wrap; gap: 6px; align-self: flex-start; }
	.tool-chip {
		font-size: 0.8rem; padding: 3px 8px; border-radius: var(--radius);
		border: 1px solid var(--border); background: var(--surface); color: var(--text-muted);
	}
	.tool-chip.error { color: var(--danger); border-color: var(--danger); }
	.error { color: var(--danger); }
	.composer { display: flex; gap: 8px; align-items: flex-end; border-top: 1px solid var(--border); padding-top: 12px; }
	textarea { flex: 1; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius); font: inherit; resize: vertical; }
</style>
