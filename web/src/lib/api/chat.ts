import { api, getCsrfToken, setCsrfToken } from '$lib/api/client';
import type { ChatEvent, Conversation, ChatMessage } from '$lib/types';

export interface ChatRequestBody {
	conversationId?: string;
	message: string;
}

// streamChat POSTs a message and yields parsed SSE ChatEvents from the response stream.
export async function* streamChat(
	body: ChatRequestBody,
	signal: AbortSignal
): AsyncIterable<ChatEvent> {
	const headers: Record<string, string> = { 'Content-Type': 'application/json' };
	const token = getCsrfToken();
	if (token) headers['X-CSRF-Token'] = token;

	const resp = await fetch('/api/chat', {
		method: 'POST',
		credentials: 'include',
		signal,
		headers,
		body: JSON.stringify(body)
	});
	const rotated = resp.headers.get('X-CSRF-Token');
	if (rotated) setCsrfToken(rotated);

	if (!resp.ok || !resp.body) {
		yield { type: 'error', message: `chat request failed (${resp.status})` };
		return;
	}

	const reader = resp.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });
			const parts = buffer.split('\n\n');
			buffer = parts.pop() ?? '';
			for (const part of parts) {
				const line = part.trim();
				if (!line.startsWith('data:')) continue;
				const json = line.slice(line.indexOf(':') + 1).trim();
				try {
					yield JSON.parse(json) as ChatEvent;
				} catch {
					/* skip malformed frame */
				}
			}
		}
	} finally {
		reader.cancel().catch(() => {});
	}
}

export const listConversations = () => api.get<Conversation[]>('/conversations');
export const getMessages = (id: string) => api.get<ChatMessage[]>(`/conversations/${id}/messages`);
export const deleteConversation = (id: string) => api.del<{ ok: boolean }>(`/conversations/${id}`);
