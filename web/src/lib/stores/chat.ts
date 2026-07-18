import { get, writable } from 'svelte/store';
import type { ChatMessage, Conversation } from '$lib/types';
import * as chatApi from '$lib/api/chat';

export const messages = writable<ChatMessage[]>([]);
export const conversations = writable<Conversation[]>([]);
export const activeId = writable<number | null>(null);
export const sending = writable(false);
export const chatError = writable<string | null>(null);

let abort: AbortController | null = null;

export function newChat(): void {
	abort?.abort();
	abort = null;
	messages.set([]);
	activeId.set(null);
	chatError.set(null);
	sending.set(false);
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
	sending.set(true);
	messages.update((m) => [...m, { role: 'user', content: text }]);
	messages.update((m) => [...m, { role: 'assistant', content: '' }]);
	const assistantIdx = get(messages).length - 1;

	abort = new AbortController();
	const body = { conversationId: get(activeId) ?? undefined, message: text };
	let convId = get(activeId);
	try {
		for await (const ev of chatApi.streamChat(body, abort.signal)) {
			if (ev.type === 'meta') {
				convId = ev.conversationId;
				if (get(activeId) === null) activeId.set(convId);
			} else if (ev.type === 'token') {
				messages.update((m) => {
					const copy = [...m];
					copy[assistantIdx] = { role: 'assistant', content: copy[assistantIdx].content + ev.delta };
					return copy;
				});
			} else if (ev.type === 'error') {
				chatError.set(ev.message);
				break;
			} else if (ev.type === 'done') {
				break;
			}
		}
	} catch {
		chatError.set('The chat stream was interrupted');
	} finally {
		sending.set(false);
		abort = null;
	}
	void refreshConversations();
	return convId;
}
