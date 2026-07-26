import { get, writable } from 'svelte/store';
import type {
	ChatAttachment,
	ChatDocumentReference,
	ChatEvent,
	ChatMessage,
	Conversation,
	CredentialRequest,
	Document,
	MessagePart
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
		messages.set(await chatApi.getMessages(id));
	} catch {
		chatError.set('Could not load conversation');
	}
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

function optimisticAttachments(files: File[]): ChatAttachment[] {
	return files.map((file, ordinal) => ({
		filename: file.name,
		mime: file.type,
		kind: file.type.startsWith('image/') ? 'image' : 'document',
		sizeBytes: file.size,
		ordinal
	}));
}

function optimisticReferences(documents: Document[]): ChatDocumentReference[] {
	return documents.map((document, ordinal) => ({
		documentId: document.id,
		filename: document.filename,
		scope: document.scope,
		ordinal,
		available: true
	}));
}

// sendMessage streams a reply; returns the conversation id (new or existing), or null on error.
export async function sendMessage(
	text: string,
	files: File[] = [],
	documentReferences: Document[] = []
): Promise<string | null> {
	if (get(sending)) return null;
	chatError.set(null);
	credentialRequest.set(null);
	sending.set(true);
	const restoreBeforeMeta = get(messages);
	const userIdx = restoreBeforeMeta.length;
	messages.update((m) => [
		...m,
		{
			role: 'user',
			content: text,
			...(files.length === 0 ? {} : { attachments: optimisticAttachments(files) }),
			...(documentReferences.length === 0
				? {}
				: { documentReferences: optimisticReferences(documentReferences) })
		}
	]);
	messages.update((m) => [...m, { role: 'assistant', content: '', parts: [] }]);
	const assistantIdx = get(messages).length - 1;
	const localAbort = beginStream();
	const body = {
		conversationId: get(activeId) ?? undefined,
		message: text,
		...(files.length === 0 ? {} : { files }),
		...(documentReferences.length === 0
			? {}
			: { documentIds: documentReferences.map((document) => document.id) })
	};
	const convId = await consumeStream(
		chatApi.streamChat(body, localAbort.signal),
		userIdx,
		assistantIdx,
		get(activeId),
		localAbort,
		restoreBeforeMeta
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
			copy[assistantIdx] = { ...current, role: 'assistant', content: textContent, parts: nextParts };
			return copy;
		});
	}

	let convId = initialConversationId;
	let receivedMeta = false;
	function streamIsActive(): boolean {
		const streamConversationId = receivedMeta ? convId : initialConversationId;
		return get(activeId) === streamConversationId;
	}
	function restoreRejectedRewrite(): void {
		if (
			!receivedMeta &&
			restoreBeforeMeta &&
			get(activeId) === initialConversationId
		) {
			messages.set(restoreBeforeMeta);
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
			copy[assistantIdx] = {
				...assistant,
				...(event.assistantMessageId === undefined ? {} : { id: event.assistantMessageId }),
				...(event.assistantContent === undefined ? {} : { content: event.assistantContent })
			};
			return copy;
		});
	}
	try {
		for await (const ev of stream) {
			if (!streamIsActive()) return null;
			if (ev.type === 'meta') {
				receivedMeta = true;
				convId = ev.conversationId;
				if (get(activeId) === null) activeId.set(convId);
				if (!streamIsActive()) return null;
				if (ev.userMessageId !== undefined) {
					messages.update((current) => {
						const copy = [...current];
						if (copy[userIdx]) {
							copy[userIdx] = {
								...copy[userIdx],
								id: ev.userMessageId,
								...(ev.attachments === undefined ? {} : { attachments: ev.attachments }),
								...(ev.documentReferences === undefined
									? {}
									: { documentReferences: ev.documentReferences })
							};
						}
						return copy;
					});
				}
			} else if (ev.type === 'token') {
				updateAssistantParts((parts) => appendTextDelta(parts, ev.delta));
			} else if (ev.type === 'tool') {
				const tool = ev.tool;
				const status = ev.status;
				if (status === 'running') {
					updateAssistantParts((parts) => [
						...parts,
						{ kind: 'tool', tool, status: 'running', arguments: ev.arguments }
					]);
				} else {
					updateAssistantParts((parts) => updateToolPart(parts, tool, status));
				}
			} else if (ev.type === 'credentials_request') {
				credentialRequest.set({
					requestId: ev.requestId,
					reason: ev.reason,
					fields: ev.fields
				});
			} else if (ev.type === 'error') {
				applyPersistedAssistant(ev);
				restoreRejectedRewrite();
				if (streamIsActive()) {
					chatError.set(ev.message);
				}
				credentialRequest.set(null);
				break;
			} else if (ev.type === 'done') {
				applyPersistedAssistant(ev);
				credentialRequest.set(null);
				break;
			}
		}
	} catch {
		// Intentional aborts should not surface as errors to the user; mark the
		// partial assistant reply as stopped instead so the UI can end cleanly.
		if (!localAbort.signal.aborted) {
			restoreRejectedRewrite();
			if (streamIsActive()) {
				chatError.set('The chat stream was interrupted');
			}
		} else if (!receivedMeta && restoreBeforeMeta) {
			// Navigation/new-chat already replaced this conversation state.
			// Only restore an unaccepted rewrite when the same conversation
			// remains active (for example, Stop was pressed before meta).
			if (get(activeId) === initialConversationId) messages.set(restoreBeforeMeta);
		} else if (streamIsActive()) {
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
	return streamIsActive() ? convId : null;
}
