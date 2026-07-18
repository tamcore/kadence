import { api, getCsrfToken, setCsrfToken } from '$lib/api/client';
import type { ChatEvent, Conversation, ChatMessage } from '$lib/types';

export interface ChatRequestBody {
	conversationId?: number;
	message: string;
}

function isCompleteJson(text: string): boolean {
	const trimmed = text.trim();
	if (!trimmed) return false;
	if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return false;

	let braces = 0;
	let brackets = 0;
	let inString = false;
	let escaped = false;

	for (const char of trimmed) {
		if (escaped) {
			escaped = false;
			continue;
		}
		if (char === '\\') {
			escaped = true;
			continue;
		}
		if (char === '"') {
			inString = !inString;
			continue;
		}
		if (inString) continue;

		if (char === '{') braces++;
		if (char === '}') braces--;
		if (char === '[') brackets++;
		if (char === ']') brackets--;
	}

	return braces === 0 && brackets === 0;
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
	let pendingJson = '';
	let inJsonAccumulation = false;

	try {
		for (;;) {
			const { done, value } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });

			// Process buffer
			let i = 0;
			while (i < buffer.length) {
				// If we're accumulating JSON, continue accumulating
				if (inJsonAccumulation) {
					// Check for end of SSE frame (\n\n)
					if (i + 1 < buffer.length && buffer[i] === '\n' && buffer[i + 1] === '\n') {
						// End of frame - try to parse pending JSON
						if (pendingJson) {
							try {
								yield JSON.parse(pendingJson) as ChatEvent;
							} catch {
								/* skip malformed frame */
							}
							pendingJson = '';
						}
						inJsonAccumulation = false;
						i += 2;
						continue;
					}

					pendingJson += buffer[i];
					i++;
					continue;
				}

				// Look for "data:" marker
				if (
					i + 5 <= buffer.length &&
					buffer[i] === 'd' &&
					buffer[i + 1] === 'a' &&
					buffer[i + 2] === 't' &&
					buffer[i + 3] === 'a' &&
					buffer[i + 4] === ':'
				) {
					i += 5;
					// Skip whitespace after "data:"
					while (i < buffer.length && (buffer[i] === ' ' || buffer[i] === '\t')) {
						i++;
					}
					// Start accumulating JSON
					inJsonAccumulation = true;
					continue;
				}

				// Skip non-data characters
				i++;
			}

			buffer = '';
		}

		// Try to parse any remaining JSON
		if (pendingJson) {
			try {
				yield JSON.parse(pendingJson) as ChatEvent;
			} catch {
				/* skip */
			}
		}
	} finally {
		reader.cancel().catch(() => {});
	}
}

export const listConversations = () => api.get<Conversation[]>('/conversations');
export const getMessages = (id: number) => api.get<ChatMessage[]>(`/conversations/${id}/messages`);
export const deleteConversation = (id: number) => api.del<{ ok: boolean }>(`/conversations/${id}`);
