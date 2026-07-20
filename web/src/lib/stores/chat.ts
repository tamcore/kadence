import { get, writable } from 'svelte/store';
import type { ChatMessage, Conversation, CredentialRequest, MessagePart } from '$lib/types';
import * as chatApi from '$lib/api/chat';

export const messages = writable<ChatMessage[]>([]);
export const conversations = writable<Conversation[]>([]);
export const activeId = writable<string | null>(null);
export const sending = writable(false);
export const chatError = writable<string | null>(null);
export const credentialRequest = writable<CredentialRequest | null>(null);

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

export async function refreshConversations(): Promise<void> {
	try {
		conversations.set(await chatApi.listConversations());
	} catch {
		/* non-fatal */
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
	chatError.set(null);
	credentialRequest.set(null);
	sending.set(true);
	messages.update((m) => [...m, { role: 'user', content: text }]);
	messages.update((m) => [...m, { role: 'assistant', content: '', parts: [] }]);
	const assistantIdx = get(messages).length - 1;

	function updateAssistantParts(update: (parts: MessagePart[]) => MessagePart[]): void {
		messages.update((m) => {
			const copy = [...m];
			const current = copy[assistantIdx];
			const nextParts = update(current.parts ?? []);
			const textContent = nextParts
				.filter((p): p is Extract<MessagePart, { kind: 'text' }> => p.kind === 'text')
				.map((p) => p.content)
				.join('');
			copy[assistantIdx] = { role: 'assistant', content: textContent, parts: nextParts };
			return copy;
		});
	}

	const localAbort = new AbortController();
	abort = localAbort;
	const body = { conversationId: get(activeId) ?? undefined, message: text };
	let convId: string | null = get(activeId);
	try {
		for await (const ev of chatApi.streamChat(body, localAbort.signal)) {
			if (ev.type === 'meta') {
				convId = ev.conversationId;
				if (get(activeId) === null) activeId.set(convId);
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
				chatError.set(ev.message);
				credentialRequest.set(null);
				break;
			} else if (ev.type === 'done') {
				credentialRequest.set(null);
				break;
			}
		}
	} catch {
		// Intentional aborts should not surface as errors to the user
		if (!localAbort.signal.aborted) {
			chatError.set('The chat stream was interrupted');
		}
		return null;
	} finally {
		// Only reset shared state if this send is still the active one
		if (abort === localAbort) {
			sending.set(false);
			abort = null;
		}
	}
	void refreshConversations();
	return convId;
}
