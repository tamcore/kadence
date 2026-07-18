import { afterEach, describe, expect, it, vi } from 'vitest';
import { streamChat } from './chat';
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

afterEach(() => vi.restoreAllMocks());

describe('streamChat', () => {
	it('parses SSE frames into ChatEvents (across chunk boundaries)', async () => {
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(streamResponse([
			'data: {"type":"meta","conversationId":7}\n\n',
			'data: {"type":"token","delta":"Hel',
			'lo"}\n\ndata: {"type":"token","delta":" world"}\n\n',
			'data: {"type":"done"}\n\n'
		])));

		const events = [];
		for await (const e of streamChat({ message: 'hi' }, new AbortController().signal)) {
			events.push(e);
		}
		expect(events[0]).toEqual({ type: 'meta', conversationId: 7 });
		expect(events.filter((e) => e.type === 'token').map((e: any) => e.delta).join('')).toBe('Hello world');
		expect(events.at(-1)).toEqual({ type: 'done' });
	});

	it('sends credentials, CSRF header, and the body', async () => {
		setCsrfToken('tok');
		const f = vi.fn().mockResolvedValue(streamResponse(['data: {"type":"done"}\n\n']));
		vi.stubGlobal('fetch', f);
		for await (const _ of streamChat({ conversationId: 3, message: 'yo' }, new AbortController().signal)) {
			/* drain */
		}
		const [url, opts] = f.mock.calls[0];
		expect(url).toBe('/api/chat');
		expect(opts.method).toBe('POST');
		expect(opts.credentials).toBe('include');
		expect(JSON.parse(opts.body)).toEqual({ conversationId: 3, message: 'yo' });
		expect(opts.headers).toHaveProperty('X-CSRF-Token');
	});
});
