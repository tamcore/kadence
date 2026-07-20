import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const streamChatMock = vi.fn();
vi.mock('$lib/api/chat', () => ({
	streamChat: (...a: unknown[]) => streamChatMock(...a),
	listConversations: vi.fn().mockResolvedValue([]),
	getMessages: vi.fn().mockResolvedValue([]),
	deleteConversation: vi.fn().mockResolvedValue({ ok: true })
}));

import { activeId, chatError, credentialRequest, messages, newChat, sendMessage, sending } from './chat';

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
			{ type: 'meta', conversationId: '11111111-1111-1111-1111-111111111111' },
			{ type: 'token', delta: 'Hel' },
			{ type: 'token', delta: 'lo' },
			{ type: 'done' }
		]));

		const id = await sendMessage('hi coach');

		expect(id).toBe('11111111-1111-1111-1111-111111111111');
		expect(get(activeId)).toBe('11111111-1111-1111-1111-111111111111');
		const msgs = get(messages);
		expect(msgs[0]).toEqual({ role: 'user', content: 'hi coach' });
		expect(msgs[1]).toEqual({
			role: 'assistant',
			content: 'Hello',
			parts: [{ kind: 'text', content: 'Hello' }]
		});
	});

	it('surfaces an error event', async () => {
		streamChatMock.mockReturnValueOnce(events([{ type: 'error', message: 'boom' }]));
		await sendMessage('x');
		expect(get(chatError)).toBe('boom');
	});

	it('does not set chatError when stream is intentionally aborted', async () => {
		// Create a stream that yields meta and pauses, allowing us to abort mid-stream
		streamChatMock.mockImplementationOnce(async function* () {
			yield { type: 'meta', conversationId: '22222222-2222-2222-2222-222222222222' };
			// Simulate a pause that allows newChat() to abort before done
			await new Promise((resolve) => setTimeout(resolve, 100));
		});

		// Start the send (will pause in the async generator)
		const sendPromise = sendMessage('hi');

		// Give it a tick to reach the await
		await new Promise((resolve) => setTimeout(resolve, 10));

		// Intentionally abort (simulating newChat())
		newChat();

		// Wait for the send to settle
		const result = await sendPromise;

		// An aborted send after receiving meta returns the convId
		expect(result).toBe('22222222-2222-2222-2222-222222222222');
		// chatError should NOT be set (was already cleared by newChat())
		expect(get(chatError)).toBeNull();
		// sending should be false
		expect(get(sending)).toBe(false);
	});

	it('transitions a running tool entry to done without duplicating it', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '33333333-3333-3333-3333-333333333333' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running', arguments: '{"days":7}' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'done' },
			{ type: 'token', delta: 'You ran 10km.' },
			{ type: 'done' }
		]));

		await sendMessage('hi');

		const assistant = get(messages)[1];
		expect(assistant.parts).toEqual([
			{ kind: 'tool', tool: 'garmin__get_activities', status: 'done', arguments: '{"days":7}' },
			{ kind: 'text', content: 'You ran 10km.' }
		]);
		expect(assistant.content).toBe('You ran 10km.');
	});

	it('places tool parts inline and in order before later text', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '33333333-3333-3333-3333-333333333333' },
			{ type: 'token', delta: 'Sure, checking...' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running', arguments: '{"days":7}' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'done' },
			{ type: 'token', delta: 'You ran 10km.' },
			{ type: 'done' }
		]));

		await sendMessage('hi');

		const assistant = get(messages)[1];
		expect(assistant.parts).toEqual([
			{ kind: 'text', content: 'Sure, checking...' },
			{ kind: 'tool', tool: 'garmin__get_activities', status: 'done', arguments: '{"days":7}' },
			{ kind: 'text', content: 'You ran 10km.' }
		]);
	});

	it('sets credentialRequest on a credentials_request event without touching messages', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '44444444-4444-4444-4444-444444444444' },
			{
				type: 'credentials_request',
				requestId: 'req-1',
				reason: 'Garmin login required',
				fields: [{ name: 'username', label: 'Username' }, { name: 'password', secret: true }]
			},
			{ type: 'token', delta: 'waiting...' }
			// Intentionally no 'done' — the store must not clear it mid-stream.
		]));

		await sendMessage('connect garmin');

		expect(get(credentialRequest)).toEqual({
			requestId: 'req-1',
			reason: 'Garmin login required',
			fields: [{ name: 'username', label: 'Username' }, { name: 'password', secret: true }]
		});
		// Only the user + assistant messages should exist — no credential entry.
		const msgs = get(messages);
		expect(msgs).toHaveLength(2);
		expect(msgs[0]).toEqual({ role: 'user', content: 'connect garmin' });
		expect(msgs[1].content).toBe('waiting...');
	});

	it('clears credentialRequest when the stream completes', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '55555555-5555-5555-5555-555555555555' },
			{
				type: 'credentials_request',
				requestId: 'req-2',
				reason: 'API key required',
				fields: [{ name: 'api_key' }]
			},
			{ type: 'done' }
		]));

		await sendMessage('go');

		expect(get(credentialRequest)).toBeNull();
	});
});
