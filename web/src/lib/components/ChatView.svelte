<script lang="ts">
	import { tick } from 'svelte';
	import { activeId, chatError, credentialRequest, messages, sendMessage, sending } from '$lib/stores/chat';
	import type { MessagePart } from '$lib/types';
	import MarkdownMessage from '$lib/components/MarkdownMessage.svelte';
	import Composer from '$lib/components/Composer.svelte';
	import CredentialPrompt from '$lib/components/CredentialPrompt.svelte';

	let { onNewConversation }: { onNewConversation?: (id: string) => void } = $props();

	let threadEl = $state<HTMLDivElement | null>(null);

	async function scrollToBottom(): Promise<void> {
		await tick();
		if (threadEl) threadEl.scrollTop = threadEl.scrollHeight;
	}

	$effect(() => {
		const lastMessage = $messages[$messages.length - 1];
		void $messages.length;
		void lastMessage?.content.length;
		void scrollToBottom();
	});

	async function submit(text: string) {
		if ($sending) return;
		const wasNew = $activeId === null;
		const id = await sendMessage(text);
		if (wasNew && id != null && onNewConversation) onNewConversation(id);
		void scrollToBottom();
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
	<div class="thread" bind:this={threadEl}>
		<div class="thread-inner">
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
	</div>

	<div class="composer-area">
		{#if $credentialRequest}
			<CredentialPrompt request={$credentialRequest} />
		{/if}
		<Composer disabled={$sending} onSubmit={(t) => submit(t)} />
	</div>
</div>

<style>
	.chat { display: flex; flex-direction: column; height: 100%; }
	.thread { flex: 1; overflow-y: auto; padding: 24px 20px 0; }
	.thread-inner {
		max-width: 760px; margin: 0 auto;
		display: flex; flex-direction: column; gap: 16px; padding-bottom: 16px;
	}
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
	.composer-area {
		flex: none;
		border-top: 1px solid var(--border);
		padding: 12px 20px calc(16px + env(safe-area-inset-bottom, 0px));
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.composer-area :global(.composer) { max-width: 760px; margin: 0 auto; width: 100%; box-sizing: border-box; }
	.composer-area :global(.credential-prompt) { max-width: 760px; margin: 0 auto; width: 100%; box-sizing: border-box; }
</style>
