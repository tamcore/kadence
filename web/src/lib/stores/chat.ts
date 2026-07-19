import { get, writable } from 'svelte/store';
import type { ChatMessage, Conversation, ToolActivity } from '$lib/types';
import * as chatApi from '$lib/api/chat';

export const messages = writable<ChatMessage[]>([]);
export const conversations = writable<Conversation[]>([]);
export const activeId = writable<number | null>(null);
export const sending = writable(false);
export const chatError = writable<string | null>(null);
export const toolActivity = writable<ToolActivity[]>([]);

let abort: AbortController | null = null;

export function newChat(): void {
	abort?.abort();
	abort = null;
	messages.set([]);
	activeId.set(null);
	chatError.set(null);
	sending.set(false);
	toolActivity.set([]);
}

export async function refreshConversations(): Promise<void> {
	try {
		conversations.set(await chatApi.listConversations());
	} catch {
		/* non-fatal */
	}
}

export async function loadConversation(id: number): Promise<void> {
	if (get(activeId) === id && get(messages).length > 0) return;
	activeId.set(id);
	chatError.set(null);
	toolActivity.set([]);
	try {
		messages.set(await chatApi.getMessages(id));
	} catch {
		chatError.set('Could not load conversation');
	}
}

export async function removeConversation(id: number): Promise<void> {
	await chatApi.deleteConversation(id);
	if (get(activeId) === id) newChat();
	await refreshConversations();
}

// sendMessage streams a reply; returns the conversation id (new or existing), or null on error.
export async function sendMessage(text: string): Promise<number | null> {
	chatError.set(null);
	toolActivity.set([]);
	sending.set(true);
	messages.update((m) => [...m, { role: 'user', content: text }]);
	messages.update((m) => [...m, { role: 'assistant', content: '' }]);
	const assistantIdx = get(messages).length - 1;

	const localAbort = new AbortController();
	abort = localAbort;
	const body = { conversationId: get(activeId) ?? undefined, message: text };
	let convId = get(activeId);
	try {
		for await (const ev of chatApi.streamChat(body, localAbort.signal)) {
			if (ev.type === 'meta') {
				convId = ev.conversationId;
				if (get(activeId) === null) activeId.set(convId);
			} else if (ev.type === 'token') {
				messages.update((m) => {
					const copy = [...m];
					copy[assistantIdx] = { role: 'assistant', content: copy[assistantIdx].content + ev.delta };
					return copy;
				});
			} else if (ev.type === 'tool') {
				toolActivity.update((list) => {
					if (ev.status === 'running') {
						return [...list, { tool: ev.tool, status: 'running' }];
					}
					// transition the most recent running entry for this tool
					const copy = [...list];
					for (let i = copy.length - 1; i >= 0; i--) {
						if (copy[i].tool === ev.tool && copy[i].status === 'running') {
							copy[i] = { tool: ev.tool, status: ev.status };
							return copy;
						}
					}
					return [...copy, { tool: ev.tool, status: ev.status }];
				});
			} else if (ev.type === 'error') {
				chatError.set(ev.message);
				break;
			} else if (ev.type === 'done') {
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
