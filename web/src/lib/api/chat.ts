import { api, getCsrfToken, handleUnauthorized, setCsrfToken } from '$lib/api/client';
import { canReachServerNow, UNREACHABLE_MESSAGE } from '$lib/stores/connection';
import { reachabilityMonitor } from '$lib/pwa/reachability-monitor';
import type { ChatEvent, Conversation, ChatMessage } from '$lib/types';

export interface ChatRequestBody {
	conversationId?: string;
	message: string;
	files?: File[];
	documentIds?: number[];
}

// streamChat POSTs a message and yields parsed SSE ChatEvents from the response stream.
export async function* streamChat(
	body: ChatRequestBody,
	signal: AbortSignal
): AsyncIterable<ChatEvent> {
	const hasRichInput = (body.files?.length ?? 0) > 0 || (body.documentIds?.length ?? 0) > 0;
	if (!hasRichInput) {
		yield* streamRequest('/api/chat', body, signal);
		return;
	}

	const form = new FormData();
	if (body.conversationId !== undefined) form.append('conversationId', body.conversationId);
	form.append('message', body.message);
	for (const file of body.files ?? []) form.append('files', file);
	for (const documentId of body.documentIds ?? []) {
		form.append('documentIds', String(documentId));
	}
	yield* streamRequest('/api/chat', form, signal);
}

export async function* editMessage(
	conversationId: string,
	messageId: number,
	message: string,
	signal: AbortSignal
): AsyncIterable<ChatEvent> {
	yield* streamRequest(
		`/api/conversations/${encodeURIComponent(conversationId)}/messages/${messageId}/edit`,
		{ message },
		signal
	);
}

export async function* regenerateMessage(
	conversationId: string,
	messageId: number,
	signal: AbortSignal
): AsyncIterable<ChatEvent> {
	yield* streamRequest(
		`/api/conversations/${encodeURIComponent(conversationId)}/messages/${messageId}/regenerate`,
		undefined,
		signal
	);
}

async function* streamRequest(
	path: string,
	body: unknown,
	signal: AbortSignal
): AsyncIterable<ChatEvent> {
	if (typeof window !== 'undefined' && !canReachServerNow()) {
		yield { type: 'error', message: UNREACHABLE_MESSAGE, transport: true };
		return;
	}

	const multipart = body instanceof FormData;
	const headers: Record<string, string> = multipart ? {} : { 'Content-Type': 'application/json' };
	const token = getCsrfToken();
	if (token) headers['X-CSRF-Token'] = token;

	let resp: Response;
	try {
		resp = await fetch(path, {
			method: 'POST',
			credentials: 'include',
			signal,
			headers,
			body: body === undefined ? undefined : multipart ? body : JSON.stringify(body)
		});
	} catch {
		if (signal.aborted) return;
		void reachabilityMonitor.probeNow();
		yield { type: 'error', message: UNREACHABLE_MESSAGE, transport: true };
		return;
	}
	const rotated = resp.headers.get('X-CSRF-Token');
	if (rotated) setCsrfToken(rotated);

	if (!resp.ok || !resp.body) {
		if (resp.status === 401) {
			handleUnauthorized();
			yield { type: 'error', message: 'unauthorized', code: 401, transport: true };
		} else {
			yield { type: 'error', message: `chat request failed (${resp.status})`, transport: true };
		}
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
export const renameConversation = (id: string, title: string) =>
	api.patch<Conversation>(`/conversations/${id}`, { title });
export const pinConversation = (id: string, pinned: boolean) =>
	api.patch<Conversation>(`/conversations/${id}`, { pinned });
export const deleteConversation = (id: string) => api.del<{ ok: boolean }>(`/conversations/${id}`);
