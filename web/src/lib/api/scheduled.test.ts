import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('$app/navigation', () => ({ goto: vi.fn() }));

import { goto } from '$app/navigation';
import {
	confirmScheduledTask,
	deleteScheduledTask,
	getScheduledTask,
	listScheduledTasks,
	markScheduledTaskRead,
	pauseScheduledTask,
	resumeScheduledTask,
	runScheduledTaskNow,
	streamScheduledDefinition
} from './scheduled';
import { setCsrfToken } from './client';

function streamResponse(chunks: string[], status = 200, headers: Record<string, string> = {}): Response {
	const body = new ReadableStream({
		start(controller) {
			const encoder = new TextEncoder();
			for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
			controller.close();
		}
	});
	return new Response(body, {
		status,
		headers: { 'Content-Type': 'text/event-stream', ...headers }
	});
}

afterEach(() => {
	vi.restoreAllMocks();
	vi.clearAllMocks();
	setCsrfToken(null);
	window.history.pushState({}, '', '/');
});

describe('Scheduled API', () => {
	it('streams typed definition events across chunk boundaries and rotates CSRF', async () => {
		setCsrfToken('before');
		const fetchMock = vi.fn().mockResolvedValue(
			streamResponse(
				[
					'data: {"type":"meta","taskId":"task-1","conversationId":"conv-1"}\n\n',
					'data: {"type":"text","delta":"When should ',
					'it run?"}\n\ndata: {"type":"task_question","question":{"id":"time","prompt":"When?","kind":"text","allowCustom":false,"optional":false}}\n\n',
					'data: {"type":"done"}\n\n'
				],
				200,
				{ 'X-CSRF-Token': 'after' }
			)
		);
		vi.stubGlobal('fetch', fetchMock);

		const events = [];
		for await (const event of streamScheduledDefinition(
			{ message: 'Review my run tomorrow' },
			new AbortController().signal
		)) {
			events.push(event);
		}

		expect(events).toEqual([
			{ type: 'meta', taskId: 'task-1', conversationId: 'conv-1' },
			{ type: 'text', delta: 'When should it run?' },
			{
				type: 'task_question',
				question: {
					id: 'time',
					prompt: 'When?',
					kind: 'text',
					allowCustom: false,
					optional: false
				}
			},
			{ type: 'done' }
		]);
		expect(fetchMock).toHaveBeenCalledWith(
			'/api/scheduled/tasks',
			expect.objectContaining({
				method: 'POST',
				credentials: 'include',
				headers: expect.objectContaining({ 'X-CSRF-Token': 'before' })
			})
		);
	});

	it('refines an existing task at its messages endpoint', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValue(streamResponse(['data: {"type":"done"}\n\n']));
		vi.stubGlobal('fetch', fetchMock);

		for await (const _ of streamScheduledDefinition(
			{ taskId: 'task-1', message: 'Weekdays at 8' },
			new AbortController().signal
		)) {
			// drain
		}

		expect(fetchMock.mock.calls[0][0]).toBe('/api/scheduled/tasks/task-1/messages');
		expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({ message: 'Weekdays at 8' });
	});

	it('bounds the SSE response and cancels its reader', async () => {
		const cancel = vi.fn().mockResolvedValue(undefined);
		const read = vi
			.fn()
			.mockResolvedValueOnce({ done: false, value: new Uint8Array(131_073) })
			.mockResolvedValueOnce({ done: true });
		vi.stubGlobal(
			'fetch',
			vi.fn().mockResolvedValue({
				ok: true,
				status: 200,
				headers: new Headers(),
				body: { getReader: () => ({ read, cancel }) }
			})
		);

		const events = [];
		for await (const event of streamScheduledDefinition(
			{ message: 'x' },
			new AbortController().signal
		)) {
			events.push(event);
		}
		expect(events).toEqual([{ type: 'error', error: 'scheduled response was too large' }]);
		expect(cancel).toHaveBeenCalled();
	});

	it('uses shared unauthorized handling for stream failures', async () => {
		window.history.pushState({}, '', '/scheduled');
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 401 })));

		const events = [];
		for await (const event of streamScheduledDefinition(
			{ message: 'x' },
			new AbortController().signal
		)) {
			events.push(event);
		}
		expect(events).toEqual([{ type: 'error', error: 'unauthorized', code: 401 }]);
		expect(goto).toHaveBeenCalledWith('/login?returnTo=' + encodeURIComponent('/scheduled'));
	});

	it('turns malformed or truncated streams into actionable errors', async () => {
		vi.stubGlobal(
			'fetch',
			vi
				.fn()
				.mockResolvedValueOnce(streamResponse(['data: {broken}\n\n']))
				.mockResolvedValueOnce(streamResponse(['data: {"type":"text","delta":"partial"}\n\n']))
		);

		const malformed = [];
		for await (const event of streamScheduledDefinition(
			{ message: 'x' },
			new AbortController().signal
		)) {
			malformed.push(event);
		}
		expect(malformed).toEqual([{ type: 'error', error: 'scheduled response was malformed' }]);

		const truncated = [];
		for await (const event of streamScheduledDefinition(
			{ message: 'x' },
			new AbortController().signal
		)) {
			truncated.push(event);
		}
		expect(truncated).toEqual([
			{ type: 'text', delta: 'partial' },
			{ type: 'error', error: 'scheduled response ended unexpectedly' }
		]);
	});

	it('maps list and confirmation calls to the lifecycle API', async () => {
		const fetchMock = vi
			.fn()
			.mockResolvedValueOnce(
				new Response(
					JSON.stringify({
						data: { tasks: [], unreadCount: 2, hasMore: true, nextOffset: 40 }
					}),
					{
					status: 200,
					headers: { 'Content-Type': 'application/json' }
					}
				)
			)
			.mockResolvedValueOnce(
				new Response(JSON.stringify({ data: { id: 'task-1', state: 'active' } }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		vi.stubGlobal('fetch', fetchMock);

		expect(await listScheduledTasks(20)).toEqual({
			tasks: [],
			unreadCount: 2,
			hasMore: true,
			nextOffset: 40
		});
		expect(fetchMock.mock.calls[0][0]).toBe('/api/scheduled/tasks?offset=20');
		expect(await confirmScheduledTask('task-1', 4)).toEqual(
			expect.objectContaining({ id: 'task-1', state: 'active' })
		);
		expect(fetchMock.mock.calls[1][0]).toBe('/api/scheduled/tasks/task-1/confirm');
		expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ expectedVersion: 4 });
	});

	it('maps detail, pause, resume, run, read, and delete to owner-scoped lifecycle endpoints', async () => {
		const response = (data: unknown) =>
			new Response(JSON.stringify({ data }), {
				status: 200,
				headers: { 'Content-Type': 'application/json' }
			});
		const fetchMock = vi
			.fn()
			.mockResolvedValueOnce(response({ task: { id: 'task/one' }, runs: [] }))
			.mockResolvedValueOnce(response({ id: 'task/one', state: 'paused' }))
			.mockResolvedValueOnce(response({ id: 'task/one', state: 'active' }))
			.mockResolvedValueOnce(response({ id: 4, state: 'pending' }))
			.mockResolvedValueOnce(response({ ok: true }))
			.mockResolvedValueOnce(response({ ok: true }));
		vi.stubGlobal('fetch', fetchMock);

		await getScheduledTask('task/one');
		await pauseScheduledTask('task/one');
		await resumeScheduledTask('task/one');
		await runScheduledTaskNow('task/one');
		await markScheduledTaskRead('task/one');
		await deleteScheduledTask('task/one');

		expect(fetchMock.mock.calls.map(([url, init]) => [url, init.method])).toEqual([
			['/api/scheduled/tasks/task%2Fone', 'GET'],
			['/api/scheduled/tasks/task%2Fone', 'PATCH'],
			['/api/scheduled/tasks/task%2Fone', 'PATCH'],
			['/api/scheduled/tasks/task%2Fone/run', 'POST'],
			['/api/scheduled/tasks/task%2Fone/read', 'POST'],
			['/api/scheduled/tasks/task%2Fone', 'DELETE']
		]);
		expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({ state: 'paused' });
		expect(JSON.parse(fetchMock.mock.calls[2][1].body)).toEqual({ state: 'active' });
	});
});
