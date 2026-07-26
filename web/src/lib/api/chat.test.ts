import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import { goto } from '$app/navigation';
import { editMessage, pinConversation, regenerateMessage, streamChat } from './chat';
import { api } from './client';
import { setCsrfToken } from './client';

function streamResponse(frames: string[]): Response {
	const body = new ReadableStream({
		start(controller) {
			const enc = new TextEncoder();
			for (const f of frames) controller.enqueue(enc.encode(f));
			controller.close();
		}
	});
	return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

afterEach(() => {
	vi.restoreAllMocks();
	vi.clearAllMocks();
	window.history.pushState({}, '', '/');
});

describe('streamChat', () => {
	it('parses SSE frames into ChatEvents (across chunk boundaries)', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(streamResponse([
			'data: {"type":"meta","conversationId":"44444444-4444-4444-4444-444444444444"}\n\n',
			'data: {"type":"token","delta":"Hel',
			'lo"}\n\ndata: {"type":"token","delta":" world"}\n\n',
			'data: {"type":"done"}\n\n'
		])));

		const events = [];
		for await (const e of streamChat({ message: 'hi' }, new AbortController().signal)) {
			events.push(e);
		}
		expect(events[0]).toEqual({
			type: 'meta',
			conversationId: '44444444-4444-4444-4444-444444444444'
		});
		expect(events.filter((e) => e.type === 'token').map((e: any) => e.delta).join('')).toBe('Hello world');
		expect(events.at(-1)).toEqual({ type: 'done' });
	});

	it('streams an edited message from its dedicated endpoint', async () => {
		const fetchMock = vi.fn().mockResolvedValue(streamResponse([
			'data: {"type":"meta","conversationId":"conv-1","userMessageId":12}\n\n',
			'data: {"type":"done","assistantMessageId":13}\n\n'
		]));
		vi.stubGlobal('fetch', fetchMock);

		const events = [];
		for await (const event of editMessage(
			'conv-1',
			12,
			'edited prompt',
			new AbortController().signal
		)) {
			events.push(event);
		}

		expect(fetchMock).toHaveBeenCalledWith(
			'/api/conversations/conv-1/messages/12/edit',
			expect.objectContaining({
				method: 'POST',
				body: JSON.stringify({ message: 'edited prompt' })
			})
		);
		expect(events).toEqual([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 12 },
			{ type: 'done', assistantMessageId: 13 }
		]);
	});

	it('streams regeneration from its dedicated endpoint', async () => {
		const fetchMock = vi.fn().mockResolvedValue(streamResponse([
			'data: {"type":"done","assistantMessageId":14}\n\n'
		]));
		vi.stubGlobal('fetch', fetchMock);

		for await (const _ of regenerateMessage('conv-1', 13, new AbortController().signal)) {
			/* drain */
		}

		expect(fetchMock).toHaveBeenCalledWith(
			'/api/conversations/conv-1/messages/13/regenerate',
			expect.objectContaining({
				method: 'POST',
				body: undefined
			})
		);
	});

	it('sends credentials, CSRF header, and the body', async () => {
		setCsrfToken('tok');
		const f = vi.fn().mockResolvedValue(streamResponse(['data: {"type":"done"}\n\n']));
		vi.stubGlobal('fetch', f);
		for await (const _ of streamChat(
			{ conversationId: '55555555-5555-5555-5555-555555555555', message: 'yo' },
			new AbortController().signal
		)) {
			/* drain */
		}
		const [url, opts] = f.mock.calls[0];
		expect(url).toBe('/api/chat');
		expect(opts.method).toBe('POST');
		expect(opts.credentials).toBe('include');
		expect(JSON.parse(opts.body)).toEqual({
			conversationId: '55555555-5555-5555-5555-555555555555',
			message: 'yo'
		});
		expect(opts.headers).toHaveProperty('X-CSRF-Token');
	});

	it('sends files and document references as ordered multipart fields without setting content-type', async () => {
		setCsrfToken('tok');
		const f = vi.fn().mockResolvedValue(streamResponse([
			'data: {"type":"meta","conversationId":"conv-1","attachments":[],"documentReferences":[]}\n\n',
			'data: {"type":"done"}\n\n'
		]));
		vi.stubGlobal('fetch', f);
		const screenshot = new File(['png'], 'finish.png', { type: 'image/png' });
		const notes = new File(['notes'], 'week.md', { type: 'text/markdown' });

		for await (const _ of streamChat(
			{
				conversationId: 'conv-1',
				message: '',
				files: [screenshot, notes],
				documentIds: [41, 72]
			},
			new AbortController().signal
		)) {
			/* drain */
		}

		const [url, opts] = f.mock.calls[0];
		expect(url).toBe('/api/chat');
		expect(opts.headers).toEqual({ 'X-CSRF-Token': 'tok' });
		expect(opts.body).toBeInstanceOf(FormData);
		const form = opts.body as FormData;
		expect(form.get('conversationId')).toBe('conv-1');
		expect(form.get('message')).toBe('');
		expect(form.getAll('files')).toEqual([screenshot, notes]);
		expect(form.getAll('documentIds')).toEqual(['41', '72']);
	});

	it('handles a 401 via the central handler and yields a single error event with no dangling reader', async () => {
		window.history.pushState({}, '', '/chat');
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(new Response(null, { status: 401 }))
		);

		const events = [];
		for await (const e of streamChat({ message: 'hi' }, new AbortController().signal)) {
			events.push(e);
		}

		expect(events).toEqual([{ type: 'error', message: 'unauthorized', code: 401 }]);
		expect(goto).toHaveBeenCalledWith('/login?returnTo=' + encodeURIComponent('/chat'));
	});

	it('yields a generic error event for a non-401 failure without touching navigation', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue(new Response(null, { status: 500 }))
		);

		const events = [];
		for await (const e of streamChat({ message: 'hi' }, new AbortController().signal)) {
			events.push(e);
		}

		expect(events).toEqual([{ type: 'error', message: 'chat request failed (500)' }]);
		expect(goto).not.toHaveBeenCalled();
	});
});

describe('conversation mutations', () => {
	it('pins a conversation through the PATCH endpoint', async () => {
		const patch = vi.spyOn(api, 'patch').mockResolvedValue({
			id: 'conv-1',
			title: 'Morning run',
			createdAt: '2026-07-26T08:00:00Z',
			lastActivityAt: '2026-07-26T09:00:00Z',
			pinnedAt: '2026-07-26T09:01:00Z'
		});

		await pinConversation('conv-1', true);

		expect(patch).toHaveBeenCalledWith('/conversations/conv-1', { pinned: true });
	});
});
