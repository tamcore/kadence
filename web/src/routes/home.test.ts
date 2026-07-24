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
		activeId: writable<string | null>(null),
		newChat: () => newChatMock()
	};
});
vi.mock('$app/navigation', () => ({ goto: (...a: unknown[]) => gotoMock(...a) }));

vi.mock('$lib/stores/auth', async () => {
	const { writable } = await import('svelte/store');
	return {
		currentUser: writable<User | null>(null)
	};
});

import Home from './+page.svelte';
import { activeId as activeIdStore } from '$lib/stores/chat';
import { currentUser } from '$lib/stores/auth';
import type { User } from '$lib/types';

function testUser(overrides: Partial<User>): User {
	return {
		id: 1,
		username: 'alice',
		email: 'alice@example.com',
		role: 'user',
		displayName: '',
		unitSystem: 'metric',
		location: '',
		aboutMe: '',
		timezone: 'UTC',
		scheduledEnabled: false,
		...overrides
	};
}

afterEach(() => {
	vi.clearAllMocks();
	activeIdStore.set(null);
	currentUser.set(null);
});

describe('home page', () => {
	it('renders a greeting and the composer', () => {
		render(Home);
		expect(screen.getByRole('heading')).toBeInTheDocument();
		expect(screen.getByRole('textbox')).toBeInTheDocument();
	});

	it('greets by displayName when set', () => {
		currentUser.set(testUser({ displayName: 'Alice' }));
		render(Home);
		expect(screen.getByRole('heading')).toHaveTextContent('What can I help with, Alice?');
	});

	it('falls back to username when displayName is empty', () => {
		currentUser.set(testUser({ displayName: '' }));
		render(Home);
		expect(screen.getByRole('heading')).toHaveTextContent('What can I help with, alice?');
	});

	it('omits the name entirely when signed out', () => {
		currentUser.set(null);
		render(Home);
		expect(screen.getByRole('heading')).toHaveTextContent('What can I help with?');
	});

	it('calls newChat on mount', () => {
		render(Home);
		expect(newChatMock).toHaveBeenCalledOnce();
	});

	it('sends the message without waiting for the stream to finish', async () => {
		let resolveSend: (id: string | null) => void = () => {};
		sendMessageMock.mockReturnValueOnce(
			new Promise((resolve) => {
				resolveSend = resolve;
			})
		);
		render(Home);
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hello coach' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(sendMessageMock).toHaveBeenCalledWith('hello coach');
		// The send promise is still pending (stream in-flight) — no navigation yet.
		expect(gotoMock).not.toHaveBeenCalled();

		resolveSend('66666666-6666-6666-6666-666666666666');
	});

	it('navigates to the conversation as soon as activeId is set, mid-stream', async () => {
		sendMessageMock.mockReturnValueOnce(new Promise(() => {})); // never resolves during test
		render(Home);
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hello coach' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(gotoMock).not.toHaveBeenCalled();

		// Simulate the `meta` stream event setting activeId while streaming continues.
		activeIdStore.set('66666666-6666-6666-6666-666666666666');
		await Promise.resolve();

		expect(gotoMock).toHaveBeenCalledWith('/chat/66666666-6666-6666-6666-666666666666');
	});

	it('does not navigate while activeId stays null', async () => {
		sendMessageMock.mockReturnValueOnce(new Promise(() => {}));
		render(Home);
		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hi' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(sendMessageMock).toHaveBeenCalledWith('hi');
		expect(gotoMock).not.toHaveBeenCalled();
	});
});
