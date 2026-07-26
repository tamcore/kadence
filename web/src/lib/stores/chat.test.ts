import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const streamChatMock = vi.fn();
const editMessageMock = vi.fn();
const regenerateMessageMock = vi.fn();
const getMessagesMock = vi.fn().mockResolvedValue([]);
const listConversationsMock = vi.fn().mockResolvedValue([]);
const renameConversationMock = vi.fn().mockResolvedValue({ id: '1', title: 'renamed' });
vi.mock('$lib/api/chat', () => ({
	streamChat: (...a: unknown[]) => streamChatMock(...a),
	editMessage: (...a: unknown[]) => editMessageMock(...a),
	regenerateMessage: (...a: unknown[]) => regenerateMessageMock(...a),
	listConversations: (...a: unknown[]) => listConversationsMock(...a),
	getMessages: (...a: unknown[]) => getMessagesMock(...a),
	renameConversation: (...a: unknown[]) => renameConversationMock(...a),
	deleteConversation: vi.fn().mockResolvedValue({ ok: true })
}));

import {
	activeId,
	chatError,
	conversationsRefreshError,
	credentialRequest,
	messages,
	newChat,
	editMessage,
	loadConversation,
	regenerateMessage,
	refreshConversations,
	renameConversation,
	sendMessage,
	sending,
	stopGeneration
} from './chat';

async function* events(evs: unknown[]) {
	for (const e of evs) yield e;
}

function navigatedConversation() {
	return [
		{ id: 11, role: 'user' as const, content: 'conversation B first prompt' },
		{ id: 12, role: 'assistant' as const, content: 'conversation B first answer' },
		{ id: 13, role: 'user' as const, content: 'conversation B current prompt' },
		{ id: 14, role: 'assistant' as const, content: 'conversation B current answer' }
	];
}

beforeEach(() => {
	newChat();
	streamChatMock.mockReset();
	editMessageMock.mockReset();
	regenerateMessageMock.mockReset();
	getMessagesMock.mockReset().mockResolvedValue([]);
	listConversationsMock.mockReset().mockResolvedValue([]);
	renameConversationMock.mockReset().mockResolvedValue({ id: '1', title: 'renamed' });
});
afterEach(() => vi.clearAllMocks());

describe('chat store', () => {
	it('sendMessage appends user msg, streams tokens, and captures the new conversation id', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '11111111-1111-1111-1111-111111111111', userMessageId: 11 },
			{ type: 'token', delta: 'Hel' },
			{ type: 'token', delta: 'lo' },
			{ type: 'done', assistantMessageId: 12 }
		]));

		const id = await sendMessage('hi coach');

		expect(id).toBe('11111111-1111-1111-1111-111111111111');
		expect(get(activeId)).toBe('11111111-1111-1111-1111-111111111111');
		const msgs = get(messages);
		expect(msgs[0]).toEqual({ id: 11, role: 'user', content: 'hi coach' });
		expect(msgs[1]).toEqual({
			id: 12,
			role: 'assistant',
			content: 'Hello',
			parts: [{ kind: 'text', content: 'Hello' }]
		});
	});

	it('reconciles optimistic file and document metadata from the persisted meta event', async () => {
		const file = new File(['image'], 'finish.png', { type: 'image/png' });
		const selectedDocument = {
			id: 41,
			filename: 'marathon-plan.pdf',
			mime: 'application/pdf',
			source_type: 'pdf',
			scope: 'private' as const,
			created_at: '2026-07-25T20:00:00Z'
		};
		const persistedAttachments = [{
			id: 91,
			filename: 'finish.png',
			mime: 'image/png',
			kind: 'image' as const,
			sizeBytes: 5,
			imageWidth: 1200,
			imageHeight: 800,
			ordinal: 0
		}];
		const persistedReferences = [{
			id: 92,
			documentId: 41,
			filename: 'marathon-plan.pdf',
			scope: 'private' as const,
			ordinal: 0,
			available: true
		}];
		streamChatMock.mockReturnValueOnce(events([
			{
				type: 'meta',
				conversationId: 'conv-1',
				userMessageId: 90,
				attachments: persistedAttachments,
				documentReferences: persistedReferences
			},
			{ type: 'done', assistantMessageId: 93 }
		]));

		await sendMessage('', [file], [selectedDocument]);

		expect(streamChatMock).toHaveBeenCalledWith(
			{
				conversationId: undefined,
				message: '',
				files: [file],
				documentIds: [41]
			},
			expect.any(AbortSignal)
		);
		expect(get(messages)[0]).toEqual({
			id: 90,
			role: 'user',
			content: '',
			attachments: persistedAttachments,
			documentReferences: persistedReferences
		});
	});

	it('hydrates loaded scheduled artifacts after canonical text in ordinal order', async () => {
		getMessagesMock.mockResolvedValueOnce([
			{
				id: 12,
				role: 'assistant',
				content: 'I delegated two follow-ups.',
				scheduledArtifacts: [
					{ handoffId: 'handoff-2', ordinal: 2, artifactState: 'ready' },
					{ handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' }
				]
			}
		]);

		await loadConversation('conv-load');

		expect(get(messages)[0].parts).toEqual([
			{ kind: 'text', content: 'I delegated two follow-ups.' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-2' }) }
		]);
	});

	it('editMessage rewinds later turns and streams a replacement', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'old prompt' },
			{ id: 4, role: 'assistant', content: 'old response' },
			{ id: 5, role: 'user', content: 'later prompt' }
		]);
		editMessageMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 3 },
			{ type: 'token', delta: 'replacement' },
			{ type: 'done', assistantMessageId: 6 }
		]));

		await editMessage(3, 'edited prompt');

		expect(editMessageMock).toHaveBeenCalledWith(
			'conv-1',
			3,
			'edited prompt',
			expect.any(AbortSignal)
		);
		expect(get(messages)).toEqual([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'edited prompt' },
			{
				id: 6,
				role: 'assistant',
				content: 'replacement',
				parts: [{ kind: 'text', content: 'replacement' }]
			}
		]);
	});

	it('regenerateMessage removes selected response and later turns before streaming', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'retry me' },
			{ id: 4, role: 'assistant', content: 'old response' },
			{ id: 5, role: 'user', content: 'later prompt' }
		]);
		regenerateMessageMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 3 },
			{ type: 'token', delta: 'new response' },
			{ type: 'done', assistantMessageId: 6 }
		]));

		await regenerateMessage(4);

		expect(regenerateMessageMock).toHaveBeenCalledWith(
			'conv-1',
			4,
			expect.any(AbortSignal)
		);
		expect(get(messages)).toEqual([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'retry me' },
			{
				id: 6,
				role: 'assistant',
				content: 'new response',
				parts: [{ kind: 'text', content: 'new response' }]
			}
		]);
	});

	it('preserves persisted attachments and references through edit and regeneration', async () => {
		const resources = {
			attachments: [{
				id: 91,
				filename: 'finish.png',
				mime: 'image/png',
				kind: 'image' as const,
				sizeBytes: 5,
				ordinal: 0
			}],
			documentReferences: [{
				id: 92,
				documentId: 41,
				filename: 'my-plan.md',
				scope: 'private' as const,
				ordinal: 0,
				available: true
			}]
		};
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'old prompt', ...resources },
			{ id: 2, role: 'assistant', content: 'old answer' }
		]);
		editMessageMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'done', assistantMessageId: 3, assistantContent: 'edited answer' }
		]));

		await editMessage(1, 'edited prompt');
		expect(get(messages)[0]).toEqual({
			id: 1,
			role: 'user',
			content: 'edited prompt',
			...resources
		});

		regenerateMessageMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'done', assistantMessageId: 4, assistantContent: 'regenerated answer' }
		]));
		await regenerateMessage(3);

		expect(get(messages)[0]).toEqual({
			id: 1,
			role: 'user',
			content: 'edited prompt',
			...resources
		});
	});

	it('restores the edit snapshot when the server rejects before meta', async () => {
		activeId.set('conv-1');
		const original = [
			{ id: 1, role: 'user' as const, content: 'first' },
			{ id: 2, role: 'assistant' as const, content: 'answer' },
			{ id: 3, role: 'user' as const, content: 'old prompt' },
			{ id: 4, role: 'assistant' as const, content: 'old response' }
		];
		messages.set(original);
		editMessageMock.mockReturnValueOnce(events([{ type: 'error', message: 'edit rejected' }]));

		await editMessage(3, 'edited prompt');

		expect(get(messages)).toEqual(original);
		expect(get(chatError)).toBe('edit rejected');
	});

	it('refetches canonical scheduled artifacts when an accepted edit stream fails', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'old prompt' },
			{ id: 4, role: 'assistant', content: 'old response' }
		]);
		editMessageMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 3 },
			{ type: 'error', message: 'provider failed' }
		]));
		getMessagesMock.mockResolvedValueOnce([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'edited prompt' },
			{
				id: 6,
				role: 'assistant',
				content: 'canonical replacement',
				scheduledArtifacts: [
					{ handoffId: 'handoff-2', ordinal: 2, artifactState: 'ready' },
					{ handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' }
				]
			}
		]);

		await editMessage(3, 'edited prompt');

		expect(getMessagesMock).toHaveBeenCalledWith('conv-1');
		expect(get(messages)[3]).toMatchObject({ id: 6, content: 'canonical replacement' });
		expect(get(messages)[3].parts).toEqual([
			{ kind: 'text', content: 'canonical replacement' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-2' }) }
		]);
		expect(get(chatError)).toBe('provider failed');
	});

	it('refetches canonical scheduled artifacts when an accepted regeneration stream fails', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' }
		]);
		regenerateMessageMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'error', message: 'provider failed' }
		]));
		getMessagesMock.mockResolvedValueOnce([
			{ id: 1, role: 'user', content: 'first' },
			{
				id: 3,
				role: 'assistant',
				content: 'canonical retry',
				scheduledArtifacts: [{ handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' }]
			}
		]);

		await regenerateMessage(2);

		expect(getMessagesMock).toHaveBeenCalledWith('conv-1');
		expect(get(messages)[1].parts).toEqual([
			{ kind: 'text', content: 'canonical retry' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) }
		]);
	});

	it('upserts scheduled artifacts independently and ignores the scheduling built-in tool chip', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'token', delta: 'I delegated this.' },
			{ type: 'tool', tool: 'kadence__draft_scheduled_task', status: 'running' },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-2', ordinal: 2, artifactState: 'ready' } },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' } },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-1', ordinal: 1, artifactState: 'failed', retryable: true, reused: true } },
			{ type: 'done', assistantMessageId: 2 }
		]));

		await sendMessage('delegate this');

		const assistant = get(messages)[1];
		expect(assistant.id).toBe(2);
		expect(assistant.scheduledArtifacts).toHaveLength(2);
		expect(assistant.scheduledArtifacts?.map((item) => item.handoffId)).toEqual(['handoff-1', 'handoff-2']);
		expect(assistant.parts).toEqual([
			{ kind: 'text', content: 'I delegated this.' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1', artifactState: 'failed' }) },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-2' }) }
		]);
	});

	it('updates an existing scheduled card in place when confirmation arrives in a later turn', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'schedule race weather' },
			{
				id: 2,
				role: 'assistant',
				content: 'Please confirm.',
				scheduledArtifacts: [
					{
						handoffId: 'handoff-1',
						taskId: 'task-1',
						ordinal: 1,
						artifactState: 'ready',
						taskState: 'draft'
					}
				]
			}
		]);
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 3 },
			{ type: 'token', delta: 'Scheduled task activated.' },
			{
				type: 'scheduled_artifact',
				scheduledArtifact: {
					handoffId: 'handoff-1',
					taskId: 'task-1',
					ordinal: 1,
					artifactState: 'ready',
					taskState: 'active'
				}
			},
			{ type: 'done', assistantMessageId: 4 }
		]));

		await sendMessage('Yes');

		expect(get(messages)[1].scheduledArtifacts).toEqual([
			expect.objectContaining({ handoffId: 'handoff-1', taskState: 'active' })
		]);
		expect(get(messages)[3].scheduledArtifacts).toBeUndefined();
		expect(get(messages)[3].content).toBe('Scheduled task activated.');
	});

	it('keeps later tokens before sorted scheduled cards when an artifact arrives mid-stream', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'token', delta: 'I delegated ' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running' },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' } },
			{ type: 'token', delta: 'both follow-ups.' },
			{ type: 'done', assistantMessageId: 2 }
		]));

		await sendMessage('delegate this');

		expect(get(messages)[1].parts).toEqual([
			{ kind: 'text', content: 'I delegated both follow-ups.' },
			{ kind: 'tool', tool: 'garmin__get_activities', status: 'running', arguments: undefined },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) }
		]);
	});

	it('keeps generic tool updates before scheduled cards after an artifact', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-2', ordinal: 2, artifactState: 'ready' } },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' } },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running', arguments: '{"days":7}' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'done' },
			{ type: 'done', assistantMessageId: 2, assistantContent: 'I delegated scheduled follow-ups.' }
		]));

		await sendMessage('delegate this');

		expect(get(messages)[1].content).toBe('I delegated scheduled follow-ups.');
		expect(get(messages)[1].parts).toEqual([
			{ kind: 'text', content: 'I delegated scheduled follow-ups.' },
			{ kind: 'tool', tool: 'garmin__get_activities', status: 'done', arguments: '{"days":7}' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-2' }) }
		]);
	});

	it('rebuilds scheduled parts from persisted error content', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 },
			{ type: 'scheduled_artifact', scheduledArtifact: { handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' } },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running', arguments: '{"days":7}' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'done' },
			{
				type: 'error',
				message: 'the assistant could not complete the response',
				assistantMessageId: 2,
				assistantContent: 'Partial canonical answer.'
			}
		]));

		await sendMessage('delegate this');

		expect(get(messages)[1].content).toBe('Partial canonical answer.');
		expect(get(messages)[1].parts).toEqual([
			{ kind: 'text', content: 'Partial canonical answer.' },
			{ kind: 'tool', tool: 'garmin__get_activities', status: 'done', arguments: '{"days":7}' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) }
		]);
	});

	it('refetches canonical artifacts and reports interruption after an accepted edit ends without a terminal event', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' }
		]);
		editMessageMock.mockReturnValueOnce(events([{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 }]));
		getMessagesMock.mockResolvedValueOnce([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 3, role: 'assistant', content: 'canonical', scheduledArtifacts: [{ handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' }] }
		]);

		await editMessage(1, 'edited');

		expect(getMessagesMock).toHaveBeenCalledWith('conv-1');
		expect(get(chatError)).toBe('The chat stream was interrupted');
		expect(get(messages)[1].parts).toEqual([
			{ kind: 'text', content: 'canonical' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) }
		]);
	});

	it('refetches canonical artifacts and reports interruption after an accepted regeneration ends without a terminal event', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' }
		]);
		regenerateMessageMock.mockReturnValueOnce(events([{ type: 'meta', conversationId: 'conv-1', userMessageId: 1 }]));
		getMessagesMock.mockResolvedValueOnce([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 3, role: 'assistant', content: 'canonical retry', scheduledArtifacts: [{ handoffId: 'handoff-1', ordinal: 1, artifactState: 'ready' }] }
		]);

		await regenerateMessage(2);

		expect(getMessagesMock).toHaveBeenCalledWith('conv-1');
		expect(get(chatError)).toBe('The chat stream was interrupted');
		expect(get(messages)[1].parts).toEqual([
			{ kind: 'text', content: 'canonical retry' },
			{ kind: 'scheduled', artifact: expect.objectContaining({ handoffId: 'handoff-1' }) }
		]);
	});

	it('does not restore an old conversation when newChat aborts an edit before meta', async () => {
		activeId.set('conv-1');
		messages.set([
			{ id: 1, role: 'user', content: 'first' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'old prompt' },
			{ id: 4, role: 'assistant', content: 'old response' }
		]);
		editMessageMock.mockImplementationOnce(async function* (
			_conversationId: string,
			_messageId: number,
			_text: string,
			signal: AbortSignal
		) {
			await new Promise((_resolve, reject) => {
				signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
			});
		});

		const editPromise = editMessage(3, 'edited prompt');
		await new Promise((resolve) => setTimeout(resolve, 10));
		newChat();
		await editPromise;

		expect(get(activeId)).toBeNull();
		expect(get(messages)).toEqual([]);
		expect(get(chatError)).toBeNull();
	});

	it('surfaces an error event', async () => {
		streamChatMock.mockReturnValueOnce(events([{ type: 'error', message: 'boom' }]));
		await sendMessage('x');
		expect(get(chatError)).toBe('boom');
	});

	it('restores the pre-send transcript when a rich turn is rejected before meta', async () => {
		const original = [
			{ id: 1, role: 'user' as const, content: 'earlier prompt' },
			{ id: 2, role: 'assistant' as const, content: 'earlier answer' }
		];
		activeId.set('conv-1');
		messages.set(original);
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'error', message: 'attachments exceed maximum upload size' }
		]));

		await sendMessage('', [new File(['image'], 'finish.png', { type: 'image/png' })]);

		expect(get(messages)).toEqual(original);
		expect(get(chatError)).toBe('attachments exceed maximum upload size');
		expect(get(sending)).toBe(false);
	});

	it('does not restore a pre-meta rejection over a newly active conversation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let release!: () => void;
		streamChatMock.mockImplementationOnce(async function* () {
			await new Promise<void>((resolve) => {
				release = resolve;
			});
			yield { type: 'error', message: 'conversation A rejected' };
		});

		const sendPromise = sendMessage('new turn in A');
		await Promise.resolve();
		const conversationB = [
			{ id: 11, role: 'user' as const, content: 'conversation B' },
			{ id: 12, role: 'assistant' as const, content: 'answer B' }
		];
		activeId.set('conv-b');
		messages.set(conversationB);
		release();
		await sendPromise;

		expect(get(activeId)).toBe('conv-b');
		expect(get(messages)).toEqual(conversationB);
		expect(get(chatError)).toBeNull();
	});

	it('does not surface a thrown stale-stream error in a newly active conversation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let release!: () => void;
		streamChatMock.mockImplementationOnce(async function* () {
			await new Promise<void>((resolve) => {
				release = resolve;
			});
			throw new Error('conversation A network failure');
		});

		const sendPromise = sendMessage('new turn in A');
		await Promise.resolve();
		const conversationB = [
			{ id: 11, role: 'user' as const, content: 'conversation B' },
			{ id: 12, role: 'assistant' as const, content: 'answer B' }
		];
		activeId.set('conv-b');
		messages.set(conversationB);
		release();
		await sendPromise;

		expect(get(activeId)).toBe('conv-b');
		expect(get(messages)).toEqual(conversationB);
		expect(get(chatError)).toBeNull();
	});

	it('ignores stale meta metadata and returns null after navigation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let release!: () => void;
		streamChatMock.mockImplementationOnce(async function* () {
			await new Promise<void>((resolve) => {
				release = resolve;
			});
			yield {
				type: 'meta',
				conversationId: 'conv-a',
				userMessageId: 99,
				attachments: [{
					id: 100,
					filename: 'stale.png',
					mime: 'image/png',
					kind: 'image',
					sizeBytes: 5,
					ordinal: 0
				}],
				documentReferences: []
			};
		});

		const sendPromise = sendMessage('new turn in A');
		await Promise.resolve();
		const conversationB = navigatedConversation();
		activeId.set('conv-b');
		messages.set(conversationB);
		release();

		await expect(sendPromise).resolves.toBeNull();
		expect(get(activeId)).toBe('conv-b');
		expect(get(messages)).toEqual(conversationB);
	});

	it('ignores stale token updates and returns null after navigation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let release!: () => void;
		streamChatMock.mockImplementationOnce(async function* () {
			await new Promise<void>((resolve) => {
				release = resolve;
			});
			yield { type: 'token', delta: 'stale A token' };
		});

		const sendPromise = sendMessage('new turn in A');
		await Promise.resolve();
		const conversationB = navigatedConversation();
		activeId.set('conv-b');
		messages.set(conversationB);
		release();

		await expect(sendPromise).resolves.toBeNull();
		expect(get(messages)).toEqual(conversationB);
	});

	it('ignores stale credential requests and returns null after navigation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let release!: () => void;
		streamChatMock.mockImplementationOnce(async function* () {
			await new Promise<void>((resolve) => {
				release = resolve;
			});
			yield {
				type: 'credentials_request',
				requestId: 'stale-request',
				reason: 'stale A credentials',
				fields: [{ name: 'password', secret: true }]
			};
		});

		const sendPromise = sendMessage('new turn in A');
		await Promise.resolve();
		const conversationB = navigatedConversation();
		activeId.set('conv-b');
		messages.set(conversationB);
		release();

		await expect(sendPromise).resolves.toBeNull();
		expect(get(messages)).toEqual(conversationB);
		expect(get(credentialRequest)).toBeNull();
	});

	it('ignores stale done metadata and returns null after navigation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let release!: () => void;
		streamChatMock.mockImplementationOnce(async function* () {
			await new Promise<void>((resolve) => {
				release = resolve;
			});
			yield {
				type: 'done',
				assistantMessageId: 101,
				assistantContent: 'stale A answer'
			};
		});

		const sendPromise = sendMessage('new turn in A');
		await Promise.resolve();
		const conversationB = navigatedConversation();
		activeId.set('conv-b');
		messages.set(conversationB);
		release();

		await expect(sendPromise).resolves.toBeNull();
		expect(get(messages)).toEqual(conversationB);
	});

	it('ignores a stale post-meta abort after navigation', async () => {
		activeId.set('conv-a');
		messages.set([
			{ id: 1, role: 'user', content: 'conversation A' },
			{ id: 2, role: 'assistant', content: 'answer A' }
		]);
		let reachedPause!: () => void;
		const paused = new Promise<void>((resolve) => {
			reachedPause = resolve;
		});
		streamChatMock.mockImplementationOnce(async function* (_body: unknown, signal: AbortSignal) {
			yield { type: 'meta', conversationId: 'conv-a', userMessageId: 3 };
			reachedPause();
			await new Promise((_resolve, reject) => {
				signal.addEventListener('abort', () =>
					reject(new DOMException('aborted', 'AbortError'))
				);
			});
		});

		const sendPromise = sendMessage('new turn in A');
		await paused;
		const conversationB = navigatedConversation();
		activeId.set('conv-b');
		messages.set(conversationB);
		stopGeneration();

		await expect(sendPromise).resolves.toBeNull();
		expect(get(messages)).toEqual(conversationB);
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

		// Navigation made this stream stale; never route back to its conversation.
		expect(result).toBeNull();
		// chatError should NOT be set (was already cleared by newChat())
		expect(get(chatError)).toBeNull();
		// sending should be false
		expect(get(sending)).toBe(false);
	});

	it('stopGeneration aborts the stream, keeps partial text, and marks the message stopped', async () => {
		streamChatMock.mockImplementationOnce(async function* (_body: unknown, signal: AbortSignal) {
			yield { type: 'meta', conversationId: '66666666-6666-6666-6666-666666666666' };
			yield { type: 'token', delta: 'partial reply' };
			await new Promise((_resolve, reject) => {
				signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
			});
		});

		const sendPromise = sendMessage('hi');
		await new Promise((resolve) => setTimeout(resolve, 10));

		stopGeneration();

		await sendPromise;

		expect(get(chatError)).toBeNull();
		expect(get(sending)).toBe(false);
		const assistant = get(messages)[1];
		expect(assistant.content).toBe('partial reply');
		expect(assistant.stopped).toBe(true);
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

	it('uses the persisted canonical text after a tool loop while preserving streamed parts', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '77777777-7777-7777-7777-777777777777' },
			{ type: 'token', delta: 'I will check that. ' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'running', arguments: '{"days":7}' },
			{ type: 'tool', tool: 'garmin__get_activities', status: 'done' },
			{ type: 'token', delta: 'Your streamed summary.' },
			{ type: 'done', assistantMessageId: 12, assistantContent: 'Your canonical saved answer.' }
		]));

		await sendMessage('hi');

		const assistant = get(messages)[1];
		expect(assistant.id).toBe(12);
		expect(assistant.content).toBe('Your canonical saved answer.');
		expect(assistant.parts).toEqual([
			{ kind: 'text', content: 'I will check that. ' },
			{ kind: 'tool', tool: 'garmin__get_activities', status: 'done', arguments: '{"days":7}' },
			{ kind: 'text', content: 'Your streamed summary.' }
		]);
	});

	it('keeps a persisted partial assistant actionable after a provider error', async () => {
		streamChatMock.mockReturnValueOnce(events([
			{ type: 'meta', conversationId: '88888888-8888-8888-8888-888888888888' },
			{ type: 'token', delta: 'partial' },
			{
				type: 'error',
				message: 'the assistant could not complete the response',
				assistantMessageId: 12,
				assistantContent: 'partial canonical'
			}
		]));

		await sendMessage('hi');

		expect(get(messages)[1]).toMatchObject({
			id: 12,
			role: 'assistant',
			content: 'partial canonical'
		});
		expect(get(chatError)).toBe('the assistant could not complete the response');
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

	it('refreshConversations clears the refresh-error flag on success', async () => {
		listConversationsMock.mockResolvedValueOnce([{ id: '1', title: 'a' }]);
		await refreshConversations();
		expect(get(conversationsRefreshError)).toBe(false);
	});

	it('refreshConversations sets the refresh-error flag on failure', async () => {
		listConversationsMock.mockRejectedValueOnce(new Error('network down'));
		await refreshConversations();
		expect(get(conversationsRefreshError)).toBe(true);
	});

	it('renameConversation calls the API and refreshes the list', async () => {
		await renameConversation('1', 'New title');
		expect(renameConversationMock).toHaveBeenCalledWith('1', 'New title');
		expect(listConversationsMock).toHaveBeenCalled();
	});

	it('renameConversation propagates the API error without swallowing it', async () => {
		renameConversationMock.mockRejectedValueOnce(new Error('title too long'));
		await expect(renameConversation('1', 'x'.repeat(100))).rejects.toThrow('title too long');
	});
});
