<script lang="ts">
	import {
		activeId,
		chatError,
		credentialRequest,
		deleteUserMessage,
		editMessage,
		messageActionPending,
		messages,
		regenerateMessage,
		sendMessage,
		sending,
		stopGeneration
	} from '$lib/stores/chat';
	import type { Document, MessagePart } from '$lib/types';
	import MarkdownMessage from '$lib/components/MarkdownMessage.svelte';
	import Composer from '$lib/components/Composer.svelte';
	import CredentialPrompt from '$lib/components/CredentialPrompt.svelte';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import MessageActions from '$lib/components/MessageActions.svelte';
	import MessageEditor from '$lib/components/MessageEditor.svelte';
	import MessageResources from '$lib/components/MessageResources.svelte';
	import ScheduledArtifactCard from '$lib/components/scheduled/ScheduledArtifactCard.svelte';
	import { page } from '$app/stores';
	import { parseMsgAnchor } from './chatview-scroll';

	let { onNewConversation }: { onNewConversation?: (id: string) => void } = $props();

	let threadEl: HTMLDivElement | null = null;
	let editingMessageId = $state<number | null>(null);
	let anchorId = $state<number | null>(null);
	let anchorConsumed = false;
	let pendingAction = $state<
		| { kind: 'edit'; messageId: number; text: string }
		| { kind: 'delete'; messageId: number }
		| { kind: 'regenerate'; messageId: number }
		| null
	>(null);

	function captureThread(node: HTMLDivElement): () => void {
		threadEl = node;
		return () => {
			if (threadEl === node) threadEl = null;
		};
	}

	$effect(() => {
		anchorId = parseMsgAnchor($page.url.hash);
	});

	$effect(() => {
		const lastMessage = $messages[$messages.length - 1];
		void $messages.length;
		void lastMessage?.content.length;
		void lastMessage?.scheduledArtifacts;
		if (anchorId !== null && !anchorConsumed) return; // deep-link: don't fight the anchor scroll until consumed
		if (threadEl) threadEl.scrollTop = threadEl.scrollHeight;
	});

	$effect(() => {
		if (anchorId === null || anchorConsumed || !threadEl) return;
		void $messages.length; // re-run as messages load
		const el = threadEl.querySelector(`#msg-${anchorId}`);
		if (el) {
			el.scrollIntoView({ block: 'center' });
			anchorConsumed = true; // scroll exactly once, when the element first appears
		}
	});

	async function submit(
		text: string,
		files: File[] = [],
		documentReferences: Document[] = []
	): Promise<boolean> {
		if ($sending) return false;
		const wasNew = $activeId === null;
		const id =
			files.length > 0 || documentReferences.length > 0
				? await sendMessage(text, files, documentReferences)
				: await sendMessage(text);
		if (wasNew && id != null && onNewConversation) onNewConversation(id);
		return id != null;
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

	function hasLaterUserMessage(index: number): boolean {
		return $messages.slice(index + 1).some((message) => message.role === 'user');
	}

	function beginEdit(messageId: number): void {
		editingMessageId = messageId;
	}

	function saveEdit(index: number, messageId: number, text: string): void {
		if (hasLaterUserMessage(index)) {
			pendingAction = { kind: 'edit', messageId, text };
			return;
		}
		editingMessageId = null;
		void editMessage(messageId, text);
	}

	function requestRegenerate(index: number, messageId: number): void {
		if (hasLaterUserMessage(index)) {
			pendingAction = { kind: 'regenerate', messageId };
			return;
		}
		void regenerateMessage(messageId);
	}

	function requestDelete(messageId: number): void {
		pendingAction = { kind: 'delete', messageId };
	}

	function confirmPending(): void {
		const action = pendingAction;
		pendingAction = null;
		editingMessageId = null;
		if (!action) return;
		if (action.kind === 'edit') {
			void editMessage(action.messageId, action.text);
		} else if (action.kind === 'delete') {
			void deleteUserMessage(action.messageId);
		} else {
			void regenerateMessage(action.messageId);
		}
	}
</script>

<div class="chat">
	<div class="thread" {@attach captureThread}>
		<div class="thread-inner" data-testid="chat-thread">
			{#each $messages as m, i (i)}
				<div class="message-block {m.role}">
					<div
						class="msg {m.role}"
						id={m.id !== undefined ? `msg-${m.id}` : undefined}
						class:editing={m.role === 'user' && m.id === editingMessageId}
						data-testid={`chat-message-${m.role}`}
					>
						{#if m.role === 'assistant'}
							{#if m.purpose === 'scheduled_delivery'}
								<div class="scheduled-badge">🔔 Scheduled result</div>
							{/if}
							{#if m.parts?.length}
								{#each m.parts as part, j (part.kind === 'scheduled'
									? `scheduled-${part.artifact.handoffId}`
									: part.kind === 'tool'
										? `tool-${part.tool}-${j}`
										: `text-${j}`)}
									{#if part.kind === 'text'}
										{#if part.content}
										<MarkdownMessage content={part.content} />
									{/if}
								{:else if part.kind === 'scheduled'}
										<ScheduledArtifactCard
											artifact={part.artifact}
											disabled={$sending || $messageActionPending || !m.id}
										/>
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
							{#if m.stopped}
								<span class="stopped-marker">Stopped</span>
							{/if}
						{:else if m.id !== undefined && m.id === editingMessageId}
							<MessageEditor
								initialText={m.content}
								disabled={$sending || $messageActionPending}
								onSave={(text) => saveEdit(i, m.id!, text)}
								onCancel={() => (editingMessageId = null)}
							/>
						{:else}
							<MessageResources
								conversationId={$activeId}
								messageId={m.id}
								attachments={m.attachments}
								documentReferences={m.documentReferences}
							/>
							{#if m.content}<p>{m.content}</p>{/if}
						{/if}
					</div>
					{#if !(m.role === 'user' && m.id === editingMessageId)}
						<MessageActions
							content={m.content}
							disabled={$sending || $messageActionPending}
							onEdit={m.role === 'user' && m.id !== undefined ? () => beginEdit(m.id!) : undefined}
							onDelete={m.role === 'user' && m.id !== undefined ? () => requestDelete(m.id!) : undefined}
							onRegenerate={m.role === 'assistant' && m.id !== undefined
								? () => requestRegenerate(i, m.id!)
								: undefined}
						/>
					{/if}
				</div>
			{/each}
			{#if $chatError}<div class="error" role="alert">{$chatError}</div>{/if}
		</div>
	</div>

	<div class="composer-area">
		{#if $credentialRequest}
			<div class="credential-column">
				<CredentialPrompt request={$credentialRequest} />
			</div>
		{/if}
		{#if $sending}
			<button class="stop-btn" type="button" onclick={stopGeneration}>Stop generating</button>
		{/if}
		<div class="composer-column" data-testid="chat-composer">
			{#key $activeId}
				<Composer
					richInput
					disabled={$sending || $messageActionPending}
					onSubmit={(text, files, documents) => submit(text, files, documents)}
				/>
			{/key}
		</div>
	</div>
</div>

<ConfirmDialog
	open={pendingAction !== null}
	title={pendingAction?.kind === 'delete' ? 'Delete this message?' : 'Rewrite this conversation?'}
	message={pendingAction?.kind === 'delete'
		? 'Delete this message and all later history? This cannot be undone.'
		: pendingAction?.kind === 'edit'
			? 'Saving this edit will permanently delete all later turns.'
			: 'Regenerating this response will permanently delete all later turns.'}
	confirmLabel={pendingAction?.kind === 'delete'
		? 'Delete'
		: pendingAction?.kind === 'edit'
			? 'Edit and continue'
			: 'Regenerate'}
	onConfirm={confirmPending}
	onCancel={() => (pendingAction = null)}
/>

<style>
	.chat {
		--chat-content-width: 1200px;
		display: flex;
		flex-direction: column;
		height: 100%;
	}
	.thread { flex: 1; overflow-y: auto; overscroll-behavior-y: contain; padding: 24px 20px 0; }
	.thread-inner {
		width: 100%; max-width: var(--chat-content-width); margin: 0 auto;
		box-sizing: border-box;
		display: flex; flex-direction: column; gap: 16px; padding-bottom: 16px;
	}
	.msg {
		min-width: 0;
		max-width: 100%;
		box-sizing: border-box;
		padding: 10px 14px;
		border-radius: var(--radius);
		overflow-wrap: anywhere;
	}
	.message-block {
		min-width: 0;
		max-width: 80%;
		display: flex;
		flex-direction: column;
	}
	.message-block.user { align-self: flex-end; align-items: flex-end; }
	.message-block.assistant { align-self: stretch; align-items: stretch; max-width: 100%; }
	.msg.user { background: var(--accent); color: var(--on-accent); }
	.msg.user.editing {
		width: 100%;
		background: var(--surface);
		color: var(--text);
		border: 1px solid var(--border);
	}
	.msg.assistant {
		background: var(--surface);
		border: 1px solid var(--border);
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
	.stopped-marker {
		align-self: flex-start;
		font-size: 0.75rem;
		color: var(--text-muted);
		font-style: italic;
	}
	.scheduled-badge {
		align-self: flex-start;
		font-size: 0.75rem;
		color: var(--text-muted);
	}
	.stop-btn {
		align-self: center;
		max-width: var(--chat-content-width);
		width: 100%;
		box-sizing: border-box;
		padding: 8px 14px;
		border: 1px solid var(--border);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--text);
		cursor: pointer;
		font: inherit;
	}
	.stop-btn:hover { background: var(--bg); }
	.composer-area {
		flex: none;
		border-top: 1px solid var(--border);
		padding: 12px 20px calc(16px + env(safe-area-inset-bottom, 0px));
		display: flex;
		flex-direction: column;
		gap: 12px;
	}
	.composer-column,
	.credential-column {
		width: 100%;
		max-width: var(--chat-content-width);
		margin: 0 auto;
		box-sizing: border-box;
	}
	.composer-column :global(.composer),
	.credential-column :global(.credential-prompt) { width: 100%; box-sizing: border-box; }

	@media (max-width: 899px) {
		.message-block { max-width: 95%; }
	}
</style>
