import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const newChatMock = vi.fn();
const removeConversationMock = vi.fn();
const gotoMock = vi.fn();
const closeSidebarMock = vi.fn();

vi.mock('$app/navigation', () => ({
	goto: (...a: unknown[]) => gotoMock(...a)
}));

vi.mock('$app/stores', async () => {
	const { writable } = await import('svelte/store');
	return {
		page: writable({ params: { id: undefined }, url: { pathname: '/chat' } })
	};
});

vi.mock('$lib/stores/chat', async () => {
	const { writable } = await import('svelte/store');
	return {
		conversations: writable([]),
		newChat: (...a: unknown[]) => newChatMock(...a),
		removeConversation: (...a: unknown[]) => removeConversationMock(...a)
	};
});

vi.mock('$lib/stores/auth', async () => {
	const { writable } = await import('svelte/store');
	return {
		currentUser: writable({ username: 'alice', role: 'member' }),
		isAdmin: writable(false),
		clearAuth: vi.fn()
	};
});

vi.mock('$lib/stores/ui', () => ({
	closeSidebar: (...a: unknown[]) => closeSidebarMock(...a)
}));

vi.mock('$lib/api/client', () => ({
	api: { logout: vi.fn().mockResolvedValue(undefined) },
	APIError: class APIError extends Error {}
}));

import Sidebar from './Sidebar.svelte';
import { conversations } from '$lib/stores/chat';
import { page } from '$app/stores';

afterEach(() => {
	vi.clearAllMocks();
	(conversations as unknown as { set: (v: unknown[]) => void }).set([]);
	(page as unknown as { set: (v: unknown) => void }).set({
		params: { id: undefined },
		url: { pathname: '/chat' }
	});
});

describe('Sidebar', () => {
	it('shows empty state text when there are no conversations', () => {
		render(Sidebar, { props: {} });
		expect(screen.getByText(/no conversations yet/i)).toBeInTheDocument();
	});

	it('renders conversation titles', () => {
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 1, title: 'First chat' },
			{ id: 2, title: 'Second chat' }
		]);
		render(Sidebar, { props: {} });
		expect(screen.getByText('First chat')).toBeInTheDocument();
		expect(screen.getByText('Second chat')).toBeInTheDocument();
	});

	it('marks the conversation matching the current route as active', () => {
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: 1, title: 'First chat' },
			{ id: 2, title: 'Second chat' }
		]);
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '2' },
			url: { pathname: '/chat/2' }
		});
		render(Sidebar, { props: {} });
		const link = screen.getByText('Second chat').closest('a');
		expect(link).toHaveClass('active');
		const otherLink = screen.getByText('First chat').closest('a');
		expect(otherLink).not.toHaveClass('active');
	});

	it('calls newChat when "New chat" is clicked', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		render(Sidebar, { props: {} });
		await fireEvent.click(screen.getByRole('button', { name: /new chat/i }));
		expect(newChatMock).toHaveBeenCalled();
	});
});
