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
		toolActivity: writable([]),
		sendMessage: (...a: unknown[]) => sendMessageMock(...a)
	};
});

import ChatView from './ChatView.svelte';
import { toolActivity } from '$lib/stores/chat';

afterEach(() => {
	vi.clearAllMocks();
	(toolActivity as unknown as { set: (v: unknown[]) => void }).set([]);
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

	it('renders a prettified running tool chip', () => {
		(toolActivity as unknown as { set: (v: unknown[]) => void }).set([
			{ tool: 'garmin__get_activities', status: 'running' }
		]);
		render(ChatView, { props: {} });
		expect(screen.getByText(/garmin/i)).toBeInTheDocument();
		expect(screen.getByText(/get activities/i)).toBeInTheDocument();
		expect(screen.getByRole('status', { name: /tool activity/i }).textContent).toContain('⏳');
	});

	it('shows the done icon when a tool finishes', () => {
		(toolActivity as unknown as { set: (v: unknown[]) => void }).set([
			{ tool: 'garmin__get_activities', status: 'done' }
		]);
		render(ChatView, { props: {} });
		expect(screen.getByRole('status', { name: /tool activity/i }).textContent).toContain('✓');
	});
});
