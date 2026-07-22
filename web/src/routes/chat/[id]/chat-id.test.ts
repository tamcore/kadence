import { cleanup, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const loadConversationMock = vi.fn();

vi.mock('$lib/stores/chat', async () => {
	const { writable } = await import('svelte/store');
	return {
		messages: writable([]),
		sending: writable(false),
		chatError: writable(null),
		activeId: writable(null),
		credentialRequest: writable(null),
		sendMessage: vi.fn(),
		stopGeneration: vi.fn(),
		loadConversation: (...a: unknown[]) => loadConversationMock(...a)
	};
});

vi.mock('$app/stores', async () => {
	const { writable } = await import('svelte/store');
	return {
		page: writable({ params: { id: '11111111-1111-1111-1111-111111111111' } })
	};
});

import ChatIdPage from './+page.svelte';
import { page } from '$app/stores';

afterEach(() => {
	cleanup();
	vi.clearAllMocks();
	(page as unknown as { set: (v: unknown) => void }).set({
		params: { id: '11111111-1111-1111-1111-111111111111' }
	});
});

describe('chat/[id]/+page.svelte', () => {
	it('loads the conversation for the route id on mount and renders ChatView', () => {
		render(ChatIdPage);
		expect(loadConversationMock).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');
		expect(screen.getByRole('textbox')).toBeInTheDocument();
	});

	it('reloads when the route id changes (client-side navigation between conversations)', async () => {
		render(ChatIdPage);
		expect(loadConversationMock).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');

		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '22222222-2222-2222-2222-222222222222' }
		});
		await Promise.resolve();

		expect(loadConversationMock).toHaveBeenCalledWith('22222222-2222-2222-2222-222222222222');
	});

	it('does not load when the id is missing', () => {
		(page as unknown as { set: (v: unknown) => void }).set({ params: { id: undefined } });
		render(ChatIdPage);
		expect(loadConversationMock).not.toHaveBeenCalled();
	});
});
