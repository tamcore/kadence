import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const sendMessageMock = vi.fn();

vi.mock('$lib/stores/chat', async () => {
	const { writable } = await import('svelte/store');
	return {
		messages: writable([{ role: 'assistant', content: '**hi**' }]),
		sending: writable(false),
		chatError: writable(null),
		activeId: writable(null),
		sendMessage: (...a: unknown[]) => sendMessageMock(...a)
	};
});

import ChatView from './ChatView.svelte';
import { messages } from '$lib/stores/chat';

afterEach(() => {
	vi.clearAllMocks();
	(messages as unknown as { set: (v: unknown[]) => void }).set([{ role: 'assistant', content: '**hi**' }]);
});

describe('ChatView', () => {
	it('renders assistant markdown', () => {
		render(ChatView, { props: {} });
		expect(screen.getByText('hi').tagName.toLowerCase()).toBe('strong');
	});

	it('calls sendMessage on submit', async () => {
		sendMessageMock.mockResolvedValueOnce(9);
		render(ChatView, { props: {} });
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hello' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));
		await waitFor(() => expect(sendMessageMock).toHaveBeenCalledWith('hello'));
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
