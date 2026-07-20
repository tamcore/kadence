import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const sendMessageMock = vi.fn();
const newChatMock = vi.fn();
const gotoMock = vi.fn();

vi.mock('$lib/stores/chat', async () => {
	const { writable } = await import('svelte/store');
	return {
		sendMessage: (...a: unknown[]) => sendMessageMock(...a),
		sending: writable(false),
		newChat: () => newChatMock()
	};
});
vi.mock('$app/navigation', () => ({ goto: (...a: unknown[]) => gotoMock(...a) }));

import Home from './+page.svelte';

afterEach(() => vi.clearAllMocks());

describe('home page', () => {
	it('renders a greeting and the composer', () => {
		render(Home);
		expect(screen.getByRole('heading')).toBeInTheDocument();
		expect(screen.getByRole('textbox')).toBeInTheDocument();
	});

	it('calls newChat on mount', () => {
		render(Home);
		expect(newChatMock).toHaveBeenCalledOnce();
	});

	it('sends the message then navigates to the new conversation', async () => {
		sendMessageMock.mockResolvedValueOnce(42);
		render(Home);
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hello coach' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(sendMessageMock).toHaveBeenCalledWith('hello coach');
		expect(gotoMock).toHaveBeenCalledWith('/chat/42');
	});

	it('does not navigate when sendMessage fails to produce an id', async () => {
		sendMessageMock.mockResolvedValueOnce(null);
		render(Home);
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hi' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(sendMessageMock).toHaveBeenCalledWith('hi');
		expect(gotoMock).not.toHaveBeenCalled();
	});
});
