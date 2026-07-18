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

afterEach(() => vi.clearAllMocks());

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
});
