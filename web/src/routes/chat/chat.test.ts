import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const newChatMock = vi.fn();
const gotoMock = vi.fn();
const sendMessageMock = vi.fn();

vi.mock('$lib/stores/chat', async () => {
	const { writable } = await import('svelte/store');
	return {
		messages: writable([]),
		sending: writable(false),
		chatError: writable(null),
		activeId: writable<string | null>(null),
		confirmRequest: writable(null),
		credentialRequest: writable(null),
		messageActionPending: writable(false),
		sendMessage: (...a: unknown[]) => sendMessageMock(...a),
		deleteUserMessage: vi.fn(),
		stopGeneration: vi.fn(),
		newChat: () => newChatMock()
	};
});
vi.mock('$app/navigation', () => ({ goto: (...a: unknown[]) => gotoMock(...a) }));
vi.mock('$lib/api/documents', () => ({
	getDocumentUploadCapabilities: vi.fn().mockResolvedValue({
		max_bytes: 10 * 1024 * 1024,
		rich_extraction: true,
		accept: 'application/pdf,.pdf,text/markdown,.md'
	}),
	listDocumentReferences: vi.fn().mockResolvedValue({ own: [], public: [] })
}));
vi.mock('$app/stores', async () => {
	const { writable } = await import('svelte/store');
	return {
		page: writable({ params: {}, url: { hash: '' } })
	};
});

import ChatPage from './+page.svelte';
import { activeId } from '$lib/stores/chat';

afterEach(() => {
	vi.clearAllMocks();
	(activeId as unknown as { set: (v: string | null) => void }).set(null);
});

describe('chat/+page.svelte', () => {
	it('starts a new chat on mount and renders ChatView', () => {
		render(ChatPage);
		expect(newChatMock).toHaveBeenCalledOnce();
		expect(screen.getByRole('textbox')).toBeInTheDocument();
	});

	it('navigates to /chat/{id} with replaceState once the first message creates a conversation', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		sendMessageMock.mockResolvedValueOnce('33333333-3333-3333-3333-333333333333');
		render(ChatPage);

		await fireEvent.input(screen.getByRole('textbox'), { target: { value: 'hello coach' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));
		await Promise.resolve();
		await Promise.resolve();

		expect(gotoMock).toHaveBeenCalledWith('/chat/33333333-3333-3333-3333-333333333333', {
			replaceState: true,
			noScroll: true,
			keepFocus: true
		});
	});
});
