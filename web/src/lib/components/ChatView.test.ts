import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import source from './ChatView.svelte?raw';

const sendMessageMock = vi.fn();
const stopGenerationMock = vi.fn();
const editMessageMock = vi.fn();
const regenerateMessageMock = vi.fn();
const streamScheduledDefinitionMock = vi.fn();

vi.mock('$lib/api/scheduled', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/api/scheduled')>()),
	streamScheduledDefinition: (...args: unknown[]) => streamScheduledDefinitionMock(...args)
}));

vi.mock('$lib/stores/chat', async () => {
	const { writable } = await import('svelte/store');
	return {
		messages: writable([{ role: 'assistant', content: '**hi**' }]),
		sending: writable(false),
		chatError: writable(null),
		activeId: writable(null),
		credentialRequest: writable(null),
		sendMessage: (...a: unknown[]) => sendMessageMock(...a),
		stopGeneration: (...a: unknown[]) => stopGenerationMock(...a),
		editMessage: (...a: unknown[]) => editMessageMock(...a),
		regenerateMessage: (...a: unknown[]) => regenerateMessageMock(...a)
	};
});

vi.mock('$app/stores', async () => {
	const { writable } = await import('svelte/store');
	return {
		page: writable({ params: {}, url: { hash: '' } })
	};
});

import ChatView from './ChatView.svelte';
import { activeId, messages, sending } from '$lib/stores/chat';

beforeEach(() => {
	Object.defineProperty(navigator, 'clipboard', {
		value: { writeText: vi.fn().mockResolvedValue(undefined) },
		configurable: true
	});
});

afterEach(() => {
	vi.clearAllMocks();
	(messages as unknown as { set: (v: unknown[]) => void }).set([{ role: 'assistant', content: '**hi**' }]);
	(sending as unknown as { set: (v: boolean) => void }).set(false);
	(activeId as unknown as { set: (v: string | null) => void }).set(null);
});

describe('ChatView', () => {
	it('allows bubbles to use 95% width on mobile while containing thread overscroll', () => {
		expect(source).toMatch(
			/@media\s*\(max-width:\s*899px\)\s*\{[\s\S]*?\.message-block\s*\{[\s\S]*?max-width:\s*95%;/
		);
		expect(source).toMatch(
			/\.thread\s*\{[\s\S]*?overflow-y:\s*auto;[\s\S]*?overscroll-behavior-y:\s*contain;/
		);
	});

	it('renders assistant markdown', () => {
		render(ChatView, { props: {} });
		expect(screen.getByText('hi').tagName.toLowerCase()).toBe('strong');
	});

	it('renders durable image previews, document downloads, and reference provenance from reload metadata', () => {
		(activeId as unknown as { set: (v: string | null) => void }).set('conv-1');
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				id: 11,
				role: 'user',
				content: 'Compare these',
				attachments: [
					{
						id: 91,
						filename: 'finish.png',
						mime: 'image/png',
						kind: 'image',
						sizeBytes: 1234,
						imageWidth: 1200,
						imageHeight: 800,
						ordinal: 0
					},
					{
						id: 92,
						filename: 'race-plan.pdf',
						mime: 'application/pdf',
						kind: 'document',
						sizeBytes: 5678,
						ordinal: 1
					}
				],
				documentReferences: [
					{
						id: 93,
						documentId: 41,
						filename: 'my-plan.md',
						scope: 'private',
						ordinal: 0,
						available: true
					},
					{
						id: 94,
						filename: 'retired-guide.pdf',
						scope: 'public',
						ordinal: 1,
						available: false
					}
				]
			}
		]);
		render(ChatView, { props: {} });

		const imagePath = '/api/conversations/conv-1/messages/11/attachments/91';
		expect(screen.getByRole('img', { name: 'finish.png' })).toHaveAttribute('src', imagePath);
		expect(screen.getByRole('link', { name: 'Open finish.png' })).toHaveAttribute('href', imagePath);
		const download = screen.getByRole('link', { name: 'Download race-plan.pdf' });
		expect(download).toHaveAttribute(
			'href',
			'/api/conversations/conv-1/messages/11/attachments/92'
		);
		expect(download).toHaveAttribute('download', 'race-plan.pdf');
		expect(screen.getByText('my-plan.md')).toBeInTheDocument();
		expect(screen.getByText('Private reference')).toBeInTheDocument();
		expect(screen.getByText('retired-guide.pdf')).toBeInTheDocument();
		expect(screen.getByText('Public reference · unavailable')).toBeInTheDocument();
	});

	it('renders optimistic attachment metadata without exposing a broken download link', () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				role: 'user',
				content: '',
				attachments: [{
					filename: 'sending.png',
					mime: 'image/png',
					kind: 'image',
					sizeBytes: 5,
					ordinal: 0
				}]
			}
		]);
		render(ChatView, { props: {} });

		expect(screen.getByText('sending.png')).toBeInTheDocument();
		expect(screen.queryByRole('link', { name: 'Open sending.png' })).not.toBeInTheDocument();
	});

	it('exposes stable geometry hooks for browser layout checks', () => {
		const { container } = render(ChatView, { props: {} });
		expect(screen.getByTestId('chat-thread')).toBeInTheDocument();
		expect(screen.getByTestId('chat-composer')).toBeInTheDocument();
		expect(container.querySelector('[data-testid="chat-message-assistant"]')).toBeInTheDocument();
	});

	it('calls sendMessage on submit', async () => {
		sendMessageMock.mockResolvedValueOnce(9);
		render(ChatView, { props: {} });
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hello' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));
		await waitFor(() => expect(sendMessageMock).toHaveBeenCalledWith('hello'));
	});

	it('restores composer input when the store rejects before meta', async () => {
		sendMessageMock.mockResolvedValueOnce(null);
		render(ChatView, { props: {} });
		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;

		await fireEvent.input(textarea, { target: { value: 'retry this' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		await waitFor(() => expect(textarea.value).toBe('retry this'));
	});

	it('does not restore rejected input into a different active conversation', async () => {
		let resolveSend: (id: string | null) => void = () => {};
		sendMessageMock.mockImplementationOnce(
			() =>
				new Promise<string | null>((resolve) => {
					resolveSend = resolve;
				})
		);
		(activeId as unknown as { set: (v: string | null) => void }).set('chat-a');
		render(ChatView, { props: {} });

		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'message for A' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));
		(activeId as unknown as { set: (v: string | null) => void }).set('chat-b');
		await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue(''));

		resolveSend(null);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.getByRole('textbox')).toHaveValue('');
	});

	it('forwards a file-only composer submission to the chat store', async () => {
		sendMessageMock.mockResolvedValueOnce('conv-1');
		const { container } = render(ChatView, { props: {} });
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const screenshot = new File(['image'], 'finish.png', { type: 'image/png' });
		Object.defineProperty(input, 'files', { configurable: true, value: [screenshot] });

		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		await waitFor(() => expect(sendMessageMock).toHaveBeenCalledWith('', [screenshot], []));
	});

	it('copies exact user text and assistant markdown', async () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 1, role: 'user', content: 'plain prompt' },
			{ id: 2, role: 'assistant', content: '**formatted** answer' }
		]);
		render(ChatView, { props: {} });

		const copyButtons = screen.getAllByRole('button', { name: 'Copy message' });
		await fireEvent.click(copyButtons[0]);
		await fireEvent.click(copyButtons[1]);

		expect(navigator.clipboard.writeText).toHaveBeenNthCalledWith(1, 'plain prompt');
		expect(navigator.clipboard.writeText).toHaveBeenNthCalledWith(2, '**formatted** answer');
	});

	it('edits current prompt inline without confirmation', async () => {
		editMessageMock.mockResolvedValueOnce('conv-1');
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 1, role: 'user', content: 'old prompt' },
			{ id: 2, role: 'assistant', content: 'answer' }
		]);
		render(ChatView, { props: {} });

		await fireEvent.click(screen.getByRole('button', { name: 'Edit message' }));
		const editor = screen.getByRole('textbox', { name: 'Edit message' });
		expect(editor).toHaveValue('old prompt');
		await fireEvent.input(editor, { target: { value: 'edited prompt' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Save edit' }));

		expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
		expect(editMessageMock).toHaveBeenCalledWith(1, 'edited prompt');
	});

	it('confirms an edit that removes a later user turn', async () => {
		editMessageMock.mockResolvedValueOnce('conv-1');
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 1, role: 'user', content: 'old prompt' },
			{ id: 2, role: 'assistant', content: 'answer' },
			{ id: 3, role: 'user', content: 'later prompt' },
			{ id: 4, role: 'assistant', content: 'later answer' }
		]);
		render(ChatView, { props: {} });

		await fireEvent.click(screen.getAllByRole('button', { name: 'Edit message' })[0]);
		await fireEvent.input(screen.getByRole('textbox', { name: 'Edit message' }), {
			target: { value: 'edited prompt' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Save edit' }));

		expect(screen.getByRole('dialog', { name: 'Rewrite this conversation?' })).toBeInTheDocument();
		expect(editMessageMock).not.toHaveBeenCalled();
		await fireEvent.click(screen.getByRole('button', { name: 'Edit and continue' }));
		expect(editMessageMock).toHaveBeenCalledWith(1, 'edited prompt');
	});

	it('regenerates current response immediately and confirms historical regeneration', async () => {
		regenerateMessageMock.mockResolvedValue('conv-1');
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 1, role: 'user', content: 'first prompt' },
			{ id: 2, role: 'assistant', content: 'first answer' },
			{ id: 3, role: 'user', content: 'current prompt' },
			{ id: 4, role: 'assistant', content: 'current answer' }
		]);
		render(ChatView, { props: {} });

		const regenerateButtons = screen.getAllByRole('button', { name: 'Regenerate response' });
		await fireEvent.click(regenerateButtons[1]);
		expect(regenerateMessageMock).toHaveBeenCalledWith(4);

		await fireEvent.click(regenerateButtons[0]);
		expect(screen.getByRole('dialog', { name: 'Rewrite this conversation?' })).toBeInTheDocument();
		expect(regenerateMessageMock).not.toHaveBeenCalledWith(2);
		await fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
		expect(regenerateMessageMock).toHaveBeenCalledWith(2);
	});

	it('renders a running tool chip with the raw tool name', () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				role: 'assistant',
				content: '',
				parts: [{ kind: 'tool', tool: 'garmin__create_strength_workout', status: 'running' }]
			}
		]);
		render(ChatView, { props: {} });
		expect(screen.getByText(/garmin · create_strength_workout/)).toBeInTheDocument();
	});

	it('shows the done icon when a tool finishes', () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				role: 'assistant',
				content: '',
				parts: [{ kind: 'tool', tool: 'garmin__get_activities', status: 'done' }]
			}
		]);
		render(ChatView, { props: {} });
		expect(screen.getByText(/✓/)).toBeInTheDocument();
	});

	it('renders durable scheduled cards and never exposes the scheduling built-in as a generic tool chip', () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				id: 12,
				role: 'assistant',
				content: 'I delegated this.',
				parts: [
					{ kind: 'text', content: 'I delegated this.' },
					{
							kind: 'scheduled',
							artifact: {
								handoffId: 'handoff-1', taskId: 'task-1', ordinal: 1,
								artifactState: 'failed', taskState: 'draft', retryable: true
							}
						}
				]
			}
		]);
		render(ChatView, { props: {} });
		expect(screen.getByText('Delegated work order')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Retry' })).toBeInTheDocument();
		expect(screen.queryByText(/kadence · draft_scheduled_task/)).not.toBeInTheDocument();
	});

	it('keeps card controllers with their handoff when scheduled parts reorder', async () => {
		streamScheduledDefinitionMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-b', conversationId: 'conv-1' };
			yield { type: 'done' };
		});
		const part = (handoffId: string, taskId: string, ordinal: number) => ({
			kind: 'scheduled' as const,
			artifact: { handoffId, taskId, ordinal, artifactState: 'ready' as const, taskState: 'draft' as const }
		});
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 12, role: 'assistant', content: 'Delegated.', parts: [{ kind: 'text', content: 'Delegated.' }, part('handoff-a', 'task-a', 1), part('handoff-b', 'task-b', 2)] }
		]);
		render(ChatView, { props: {} });
		await waitFor(() => expect(document.querySelector('[data-handoff-id="handoff-a"]')).toBeInTheDocument());
		await new Promise((resolve) => setTimeout(resolve, 0));

		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 12, role: 'assistant', content: 'Delegated.', parts: [{ kind: 'text', content: 'Delegated.' }, part('handoff-b', 'task-b', 1), part('handoff-a', 'task-a', 2)] }
		]);
		const handoffB = document.querySelector('[data-handoff-id="handoff-b"]') as HTMLElement;
		await fireEvent.click(within(handoffB).getByRole('button', { name: 'Adjust' }));
		await fireEvent.input(within(handoffB).getByRole('textbox', { name: 'Adjust scheduled task' }), {
			target: { value: 'Change B.' }
		});
		await fireEvent.click(within(handoffB).getByRole('button', { name: 'Save adjustment' }));
		expect(streamScheduledDefinitionMock).toHaveBeenCalledWith(
			expect.objectContaining({ taskId: 'task-b', message: 'Change B.' }),
			expect.any(AbortSignal)
		);
	});

	it('expands the payload panel when a tool bubble with arguments is clicked', async () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				role: 'assistant',
				content: '',
				parts: [
					{
						kind: 'tool',
						tool: 'garmin__create_strength_workout',
						status: 'done',
						arguments: '{"name":"Leg day"}'
					}
				]
			}
		]);
		render(ChatView, { props: {} });
		expect(screen.getByText(/"name": "Leg day"/)).not.toBeVisible();
		await fireEvent.click(screen.getByText(/garmin · create_strength_workout/));
		expect(screen.getByText(/"name": "Leg day"/)).toBeVisible();
	});

	it('scrolls the thread to the bottom when a new message arrives', async () => {
		const { container } = render(ChatView, { props: {} });
		const threadEl = container.querySelector('.thread') as HTMLDivElement;

		Object.defineProperty(threadEl, 'scrollHeight', { value: 500, configurable: true });
		threadEl.scrollTop = 0;

		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ role: 'assistant', content: '**hi**' },
			{ role: 'user', content: 'hello there' }
		]);

		await waitFor(() => expect(threadEl.scrollTop).toBe(500));
	});

	it('shows a stop button only while sending, and it calls stopGeneration', async () => {
		(sending as unknown as { set: (v: boolean) => void }).set(true);
		render(ChatView, { props: {} });
		const stopButton = screen.getByRole('button', { name: /stop generating/i });
		await fireEvent.click(stopButton);
		expect(stopGenerationMock).toHaveBeenCalled();
	});

	it('does not show a stop button when not sending', () => {
		render(ChatView, { props: {} });
		expect(screen.queryByRole('button', { name: /stop generating/i })).not.toBeInTheDocument();
	});

	it('shows a stopped marker on an aborted assistant reply', () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{ role: 'assistant', content: 'partial reply', stopped: true }
		]);
		render(ChatView, { props: {} });
		expect(screen.getByText('Stopped')).toBeInTheDocument();
	});

	it('renders tool parts before later text parts, in order', () => {
		(messages as unknown as { set: (v: unknown[]) => void }).set([
			{
				role: 'assistant',
				content: 'All done.',
				parts: [
					{ kind: 'tool', tool: 'garmin__get_activities', status: 'done' },
					{ kind: 'text', content: 'All done.' }
				]
			}
		]);
		render(ChatView, { props: {} });
		const msg = screen.getByText(/get_activities/).closest('.msg');
		expect(msg?.textContent?.indexOf('get_activities')).toBeLessThan(
			msg?.textContent?.indexOf('All done.') ?? -1
		);
	});
});
