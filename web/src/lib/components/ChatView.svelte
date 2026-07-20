<script lang="ts">
	import { activeId, chatError, messages, sendMessage, sending } from '$lib/stores/chat';
	import type { MessagePart } from '$lib/types';
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

	function toolLabel(name: string): string {
		const [server, ...rest] = name.split('__');
		return rest.length ? `${server} · ${rest.join('__')}` : name;
	}
	function statusIcon(status: string): string {
		return status === 'done' ? '✓' : status === 'error' ? '✗' : '⏳';
	}
	function formatArguments(raw: string): string {
		try {
			return JSON.stringify(JSON.parse(raw), null, 2);
		} catch {
			return raw;
		}
	}
</script>

<div class="chat">
	<div class="thread">
		{#each $messages as m, i (i)}
			<div class="msg {m.role}">
				{#if m.role === 'assistant'}
					{#if m.parts?.length}
						{#each m.parts as part, j (j)}
							{#if part.kind === 'text'}
								{#if part.content}
									<MarkdownMessage content={part.content} />
								{/if}
							{:else}
								{@const toolPart = part as Extract<MessagePart, { kind: 'tool' }>}
								{#if toolPart.arguments}
									<details class="tool-chip {toolPart.status}">
										<summary>{statusIcon(toolPart.status)} {toolLabel(toolPart.tool)}</summary>
										<pre class="tool-payload">{formatArguments(toolPart.arguments)}</pre>
									</details>
								{:else}
									<span class="tool-chip {toolPart.status} not-expandable">
										{statusIcon(toolPart.status)} {toolLabel(toolPart.tool)}
									</span>
								{/if}
							{/if}
						{/each}
					{:else}
						<MarkdownMessage content={m.content} />
					{/if}
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
	.msg.assistant {
		align-self: flex-start; background: var(--surface); border: 1px solid var(--border);
		display: flex; flex-direction: column; gap: 8px;
	}
	.msg p { margin: 0; }
	.tool-chip {
		font-size: 0.8rem; border-radius: var(--radius); align-self: flex-start;
		border: 1px solid var(--border); background: var(--bg); color: var(--text-muted);
	}
	span.tool-chip { padding: 3px 8px; display: inline-block; }
	details.tool-chip summary {
		padding: 3px 8px; cursor: pointer; list-style: none;
	}
	details.tool-chip summary::-webkit-details-marker { display: none; }
	details.tool-chip summary::before { content: '▸ '; }
	details.tool-chip[open] summary::before { content: '▾ '; }
	.tool-chip.error { color: var(--danger); border-color: var(--danger); }
	.tool-payload {
		margin: 0 8px 6px; padding: 8px; font-size: 0.75rem; font-family: monospace;
		background: var(--bg); border: 1px solid var(--border);
		border-radius: var(--radius); overflow-x: auto; white-space: pre-wrap; word-break: break-word;
	}
	.error { color: var(--danger); }
	.composer { display: flex; gap: 8px; align-items: flex-end; border-top: 1px solid var(--border); padding-top: 12px; }
	textarea { flex: 1; padding: 10px 12px; border: 1px solid var(--border); border-radius: var(--radius); font: inherit; resize: vertical; }
</style>
