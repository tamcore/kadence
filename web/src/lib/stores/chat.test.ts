import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const streamChatMock = vi.fn();
vi.mock('$lib/api/chat', () => ({
	streamChat: (...a: unknown[]) => streamChatMock(...a),
	listConversations: vi.fn().mockResolvedValue([]),
	getMessages: vi.fn().mockResolvedValue([]),
	deleteConversation: vi.fn().mockResolvedValue({ ok: true })
}));

import { activeId, chatError, messages, newChat, sendMessage, sending, toolActivity } from './chat';

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

	it('does not set chatError when stream is intentionally aborted', async () => {
		// Create a stream that yields meta and pauses, allowing us to abort mid-stream
		streamChatMock.mockImplementationOnce(async function* () {
			yield { type: 'meta', conversationId: 99 };
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
		expect(result).toBe(99);
		// chatError should NOT be set (was already cleared by newChat())
		expect(get(chatError)).toBeNull();
		// sending should be false
		expect(get(sending)).toBe(false);
	});

	it('transitions a running tool entry to done without duplicating it', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 1 },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'done' },
			{ type: 'token', delta: 'You ran 10km.' },
			{ type: 'done' }
		]));

		await sendMessage('hi');

		expect(get(toolActivity)).toEqual([{ tool: 'garmin__get_activities', status: 'done' }]);
	});

	it('clears toolActivity at the start of a new sendMessage', async () => {
		toolActivity.set([{ tool: 'garmin__get_activities', status: 'running' }]);
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 1 },
			{ type: 'token', delta: 'hi' },
			{ type: 'done' }
		]));

		await sendMessage('hi');

		expect(get(toolActivity)).toEqual([]);
	});
});
