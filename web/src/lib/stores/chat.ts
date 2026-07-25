import { get, writable } from 'svelte/store';
import type {
	ChatEvent,
	ChatMessage,
	Conversation,
	CredentialRequest,
	MessagePart,
	ScheduledArtifact
} from '$lib/types';
import * as chatApi from '$lib/api/chat';

export const messages = writable<ChatMessage[]>([]);
export const conversations = writable<Conversation[]>([]);
export const activeId = writable<string | null>(null);
export const sending = writable(false);
export const chatError = writable<string | null>(null);
export const credentialRequest = writable<CredentialRequest | null>(null);
// conversationsRefreshError is set when a background/foreground refresh of the
// conversation list fails, so the sidebar can show an unobtrusive hint instead
// of silently leaving the list stale/empty.
export const conversationsRefreshError = writable(false);

let abort: AbortController | null = null;

export function newChat(): void {
	abort?.abort();
	abort = null;
	messages.set([]);
	activeId.set(null);
	chatError.set(null);
	sending.set(false);
	credentialRequest.set(null);
}

// stopGeneration aborts the in-flight stream, if any. The partial assistant
// reply is kept and marked as stopped rather than discarded or surfaced as
// an error (see the abort branch in sendMessage's catch).
export function stopGeneration(): void {
	abort?.abort();
}

export async function refreshConversations(): Promise<void> {
	try {
		conversations.set(await chatApi.listConversations());
		conversationsRefreshError.set(false);
	} catch {
		// Non-fatal: keep whatever list is already shown, but let the sidebar
		// surface a hint rather than failing silently.
		conversationsRefreshError.set(true);
	}
}

export async function loadConversation(id: string): Promise<void> {
	// Already live/loaded — this covers the just-created conversation from the
	// home composer, whose in-flight stream must not be clobbered by a refetch.
	if (get(activeId) === id) return;
	activeId.set(id);
	chatError.set(null);
	try {
		messages.set((await chatApi.getMessages(id)).map(hydrateMessage));
	} catch {
		chatError.set('Could not load conversation');
	}
}

function sortArtifacts(artifacts: ScheduledArtifact[]): ScheduledArtifact[] {
	return [...artifacts].sort((a, b) => a.ordinal - b.ordinal);
}

function textFirstParts(message: ChatMessage, artifacts: ScheduledArtifact[]): MessagePart[] {
	const transientTools = (message.parts ?? []).filter(
		(part): part is Extract<MessagePart, { kind: 'tool' }> => part.kind === 'tool'
	);
	return [
		{ kind: 'text', content: message.content },
		...transientTools,
		...sortArtifacts(artifacts).map((artifact) => ({ kind: 'scheduled' as const, artifact }))
	];
}

function hydrateMessage(message: ChatMessage): ChatMessage {
	if (message.role !== 'assistant') return message;
	const scheduledArtifacts = sortArtifacts(message.scheduledArtifacts ?? []);
	return {
		...message,
		...(scheduledArtifacts.length ? { scheduledArtifacts } : {}),
		parts: textFirstParts(message, scheduledArtifacts)
	};
}

// upsertScheduledPart keeps durable handoffs independent by ID. Their order is
// canonical (text first, then ordinal), even when a live stream included tools.
export function upsertScheduledPart(message: ChatMessage, artifact: ScheduledArtifact): ChatMessage {
	const existing = message.scheduledArtifacts ?? [];
	const index = existing.findIndex((item) => item.handoffId === artifact.handoffId);
	const scheduledArtifacts = sortArtifacts(
		index < 0
			? [...existing, artifact]
			: existing.map((item, itemIndex) => (itemIndex === index ? artifact : item))
	);
	return {
		...message,
		scheduledArtifacts,
		parts: textFirstParts(message, scheduledArtifacts)
	};
}

export async function removeConversation(id: string): Promise<void> {
	await chatApi.deleteConversation(id);
	if (get(activeId) === id) newChat();
	await refreshConversations();
}

export async function renameConversation(id: string, title: string): Promise<void> {
	await chatApi.renameConversation(id, title);
	await refreshConversations();
}

// appendTextDelta returns a copy of parts with delta appended to the trailing
// text part, adding a new text part first if the last part is a tool (or none exist).
function appendTextDelta(parts: MessagePart[], delta: string): MessagePart[] {
	const last = parts[parts.length - 1];
	if (last && last.kind === 'text') {
		const copy = [...parts];
		copy[copy.length - 1] = { kind: 'text', content: last.content + delta };
		return copy;
	}
	return [...parts, { kind: 'text', content: delta }];
}

// updateToolPart returns a copy of parts with the most recent running part for
// `tool` transitioned to `status`, or appends a new tool part if none is running.
function updateToolPart(
	parts: MessagePart[],
	tool: string,
	status: 'done' | 'error'
): MessagePart[] {
	for (let i = parts.length - 1; i >= 0; i--) {
		const part = parts[i];
		if (part.kind === 'tool' && part.tool === tool && part.status === 'running') {
			const copy = [...parts];
			copy[i] = { ...part, status };
			return copy;
		}
	}
	return [...parts, { kind: 'tool', tool, status }];
}

// sendMessage streams a reply; returns the conversation id (new or existing), or null on error.
export async function sendMessage(text: string): Promise<string | null> {
	if (get(sending)) return null;
	chatError.set(null);
	credentialRequest.set(null);
	sending.set(true);
	const userIdx = get(messages).length;
	messages.update((m) => [...m, { role: 'user', content: text }]);
	messages.update((m) => [...m, { role: 'assistant', content: '', parts: [] }]);
	const assistantIdx = get(messages).length - 1;
	const localAbort = beginStream();
	const body = { conversationId: get(activeId) ?? undefined, message: text };
	const convId = await consumeStream(
		chatApi.streamChat(body, localAbort.signal),
		userIdx,
		assistantIdx,
		get(activeId),
		localAbort
	);
	if (convId != null) void refreshConversations();
	return convId;
}

// editMessage rewinds the local transcript at one persisted user message and
// streams its server-side replacement turn.
export async function editMessage(messageId: number, text: string): Promise<string | null> {
	if (get(sending)) return null;
	const conversationId = get(activeId);
	const current = get(messages);
	const userIdx = current.findIndex((message) => message.id === messageId && message.role === 'user');
	if (conversationId == null || userIdx < 0) {
		chatError.set('Could not edit message');
		return null;
	}

	chatError.set(null);
	credentialRequest.set(null);
	sending.set(true);
	messages.set([
		...current.slice(0, userIdx),
		{ ...current[userIdx], content: text },
		{ role: 'assistant', content: '', parts: [] }
	]);
	const assistantIdx = userIdx + 1;
	const localAbort = beginStream();
	const convId = await consumeStream(
		chatApi.editMessage(conversationId, messageId, text, localAbort.signal),
		userIdx,
		assistantIdx,
		conversationId,
		localAbort,
		current
	);
	if (convId != null) void refreshConversations();
	return convId;
}

// regenerateMessage rewinds the local transcript before one persisted
// assistant response and streams its replacement.
export async function regenerateMessage(messageId: number): Promise<string | null> {
	if (get(sending)) return null;
	const conversationId = get(activeId);
	const current = get(messages);
	const assistantIdx = current.findIndex(
		(message) => message.id === messageId && message.role === 'assistant'
	);
	if (
		conversationId == null ||
		assistantIdx <= 0 ||
		current[assistantIdx - 1].role !== 'user'
	) {
		chatError.set('Could not regenerate response');
		return null;
	}

	chatError.set(null);
	credentialRequest.set(null);
	sending.set(true);
	messages.set([
		...current.slice(0, assistantIdx),
		{ role: 'assistant', content: '', parts: [] }
	]);
	const localAbort = beginStream();
	const convId = await consumeStream(
		chatApi.regenerateMessage(conversationId, messageId, localAbort.signal),
		assistantIdx - 1,
		assistantIdx,
		conversationId,
		localAbort,
		current
	);
	if (convId != null) void refreshConversations();
	return convId;
}

function beginStream(): AbortController {
	const localAbort = new AbortController();
	abort = localAbort;
	return localAbort;
}

async function consumeStream(
	stream: AsyncIterable<ChatEvent>,
	userIdx: number,
	assistantIdx: number,
	initialConversationId: string | null,
	localAbort: AbortController,
	restoreBeforeMeta?: ChatMessage[]
): Promise<string | null> {
	function updateAssistantParts(update: (parts: MessagePart[]) => MessagePart[]): void {
		messages.update((m) => {
			const copy = [...m];
			const current = copy[assistantIdx];
			if (!current) return m;
			const nextParts = update(current.parts ?? []);
			const textContent = nextParts
				.filter((p): p is Extract<MessagePart, { kind: 'text' }> => p.kind === 'text')
				.map((p) => p.content)
				.join('');
			const scheduledArtifacts = current.scheduledArtifacts ?? [];
			copy[assistantIdx] = {
				...current,
				role: 'assistant',
				content: textContent,
				parts: scheduledArtifacts.length
					? textFirstParts({ ...current, content: textContent, parts: nextParts }, scheduledArtifacts)
					: nextParts
			};
			return copy;
		});
	}
	function appendAssistantToken(delta: string): void {
		messages.update((current) => {
			const copy = [...current];
			const assistant = copy[assistantIdx];
			if (!assistant) return current;
			const content = assistant.content + delta;
			const scheduledArtifacts = assistant.scheduledArtifacts ?? [];
			copy[assistantIdx] = {
				...assistant,
				content,
				parts: scheduledArtifacts.length
					? textFirstParts({ ...assistant, content }, scheduledArtifacts)
					: appendTextDelta(assistant.parts ?? [], delta)
			};
			return copy;
		});
	}

	let convId = initialConversationId;
	let receivedMeta = false;
	let receivedTerminal = false;
	function restoreRejectedRewrite(): void {
		if (!receivedMeta && restoreBeforeMeta) messages.set(restoreBeforeMeta);
	}
	async function refetchAcceptedRewrite(): Promise<void> {
		if (!receivedMeta || !restoreBeforeMeta || initialConversationId === null) return;
		if (get(activeId) !== initialConversationId) return;
		try {
			const canonical = await chatApi.getMessages(initialConversationId);
			if (get(activeId) === initialConversationId) messages.set(canonical.map(hydrateMessage));
		} catch {
			// Keep the locally streamed rewrite when its canonical reload is unavailable.
		}
	}
	function applyPersistedAssistant(
		event: Extract<ChatEvent, { type: 'done' | 'error' }>
	): void {
		if (event.assistantMessageId === undefined && event.assistantContent === undefined) return;
		messages.update((current) => {
			const copy = [...current];
			const assistant = copy[assistantIdx];
			if (!assistant) return current;
			const content = event.assistantContent ?? assistant.content;
			const scheduledArtifacts = assistant.scheduledArtifacts ?? [];
			copy[assistantIdx] = {
				...assistant,
				...(event.assistantMessageId === undefined ? {} : { id: event.assistantMessageId }),
				content,
				parts: scheduledArtifacts.length
					? textFirstParts({ ...assistant, content }, scheduledArtifacts)
					: assistant.parts
			};
			return copy;
		});
	}
	try {
		for await (const ev of stream) {
			if (ev.type === 'meta') {
				receivedMeta = true;
				convId = ev.conversationId;
				if (get(activeId) === null) activeId.set(convId);
				if (ev.userMessageId !== undefined) {
					messages.update((current) => {
						const copy = [...current];
						if (copy[userIdx]) copy[userIdx] = { ...copy[userIdx], id: ev.userMessageId };
						return copy;
					});
				}
			} else if (ev.type === 'token') {
				appendAssistantToken(ev.delta);
			} else if (ev.type === 'tool') {
				const tool = ev.tool;
				const status = ev.status;
				if (tool === 'kadence__draft_scheduled_task') continue;
				if (status === 'running') {
					updateAssistantParts((parts) => [
						...parts,
						{ kind: 'tool', tool, status: 'running', arguments: ev.arguments }
					]);
				} else {
					updateAssistantParts((parts) => updateToolPart(parts, tool, status));
				}
			} else if (ev.type === 'scheduled_artifact') {
				messages.update((current) => {
					const copy = [...current];
					const assistant = copy[assistantIdx];
					if (!assistant) return current;
					copy[assistantIdx] = upsertScheduledPart(assistant, ev.scheduledArtifact);
					return copy;
				});
			} else if (ev.type === 'credentials_request') {
				credentialRequest.set({
					requestId: ev.requestId,
					reason: ev.reason,
					fields: ev.fields
				});
			} else if (ev.type === 'error') {
				receivedTerminal = true;
				applyPersistedAssistant(ev);
				restoreRejectedRewrite();
				await refetchAcceptedRewrite();
				chatError.set(ev.message);
				credentialRequest.set(null);
				break;
			} else if (ev.type === 'done') {
				receivedTerminal = true;
				applyPersistedAssistant(ev);
				credentialRequest.set(null);
				break;
			}
		}
		if (receivedMeta && !receivedTerminal && restoreBeforeMeta) {
			await refetchAcceptedRewrite();
			chatError.set('The chat stream was interrupted');
			credentialRequest.set(null);
			return null;
		}
	} catch {
		// Intentional aborts should not surface as errors to the user; mark the
		// partial assistant reply as stopped instead so the UI can end cleanly.
		if (!localAbort.signal.aborted) {
			restoreRejectedRewrite();
			await refetchAcceptedRewrite();
			chatError.set('The chat stream was interrupted');
		} else if (!receivedMeta && restoreBeforeMeta) {
			// Navigation/new-chat already replaced this conversation state.
			// Only restore an unaccepted rewrite when the same conversation
			// remains active (for example, Stop was pressed before meta).
			if (get(activeId) === initialConversationId) messages.set(restoreBeforeMeta);
		} else {
			messages.update((m) => {
				const copy = [...m];
				const current = copy[assistantIdx];
				if (current) copy[assistantIdx] = { ...current, stopped: true };
				return copy;
			});
			credentialRequest.set(null);
		}
		return null;
	} finally {
		// Only reset shared state if this send is still the active one
		if (abort === localAbort) {
			sending.set(false);
			abort = null;
		}
	}
	return convId;
}
