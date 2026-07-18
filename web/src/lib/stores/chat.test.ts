import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const streamChatMock = vi.fn();
vi.mock('$lib/api/chat', () => ({
	streamChat: (...a: unknown[]) => streamChatMock(...a),
	listConversations: vi.fn().mockResolvedValue([]),
	getMessages: vi.fn().mockResolvedValue([]),
	deleteConversation: vi.fn().mockResolvedValue({ ok: true })
}));

import { activeId, chatError, messages, newChat, sendMessage } from './chat';

async function* events(evs: unknown[]) {
	for (const e of evs) yield e;
}

beforeEach(() => {
	newChat();
	streamChatMock.mockReset();
});
afterEach(() => vi.clearAllMocks());

describe('chat store', () => {
	it('sendMessage appends user msg, streams tokens, and captures the new conversation id', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 42 },
			{ type: 'token', delta: 'Hel' },
			{ type: 'token', delta: 'lo' },
			{ type: 'done' }
		]));

		const id = await sendMessage('hi coach');

		expect(id).toBe(42);
		expect(get(activeId)).toBe(42);
		const msgs = get(messages);
		expect(msgs[0]).toEqual({ role: 'user', content: 'hi coach' });
		expect(msgs[1]).toEqual({ role: 'assistant', content: 'Hello' });
	});

	it('surfaces an error event', async () => {
		streamChatMock.mockReturnValueOnce(events([{ type: 'error', message: 'boom' }]));
		await sendMessage('x');
		expect(get(chatError)).toBe('boom');
	});
});
