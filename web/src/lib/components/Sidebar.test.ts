import { render, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const newChatMock = vi.fn();
const removeConversationMock = vi.fn();
const renameConversationMock = vi.fn();
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
		conversationsRefreshError: writable(false),
		newChat: (...a: unknown[]) => newChatMock(...a),
		refreshConversations: vi.fn().mockResolvedValue(undefined),
		removeConversation: (...a: unknown[]) => removeConversationMock(...a),
		renameConversation: (...a: unknown[]) => renameConversationMock(...a)
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
import { conversations, conversationsRefreshError } from '$lib/stores/chat';
import { page } from '$app/stores';

afterEach(() => {
	vi.clearAllMocks();
	(conversations as unknown as { set: (v: unknown[]) => void }).set([]);
	(conversationsRefreshError as unknown as { set: (v: boolean) => void }).set(false);
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
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' },
			{ id: '22222222-2222-2222-2222-222222222222', title: 'Second chat' }
		]);
		render(Sidebar, { props: {} });
		expect(screen.getByText('First chat')).toBeInTheDocument();
		expect(screen.getByText('Second chat')).toBeInTheDocument();
	});

	it('marks the conversation matching the current route as active', () => {
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' },
			{ id: '22222222-2222-2222-2222-222222222222', title: 'Second chat' }
		]);
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '22222222-2222-2222-2222-222222222222' },
			url: { pathname: '/chat/22222222-2222-2222-2222-222222222222' }
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

	it('asks for confirmation before deleting, and cancel keeps the conversation', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' }
		]);
		render(Sidebar, { props: {} });
		await fireEvent.click(screen.getByRole('button', { name: /delete conversation/i }));
		expect(await screen.findByRole('dialog', { name: 'Delete conversation' })).toBeInTheDocument();

		await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		expect(removeConversationMock).not.toHaveBeenCalled();
		expect(screen.getByText('First chat')).toBeInTheDocument();
	});

	it('navigates home and closes the drawer when deleting the active conversation', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		removeConversationMock.mockResolvedValueOnce(undefined);
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' }
		]);
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '11111111-1111-1111-1111-111111111111' },
			url: { pathname: '/chat/11111111-1111-1111-1111-111111111111' }
		});
		render(Sidebar, { props: {} });
		await fireEvent.click(screen.getByRole('button', { name: /delete conversation/i }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

		expect(removeConversationMock).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');
		expect(gotoMock).toHaveBeenCalledWith('/');
		expect(closeSidebarMock).toHaveBeenCalled();
	});

	it('does not navigate when deleting a non-active conversation', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		removeConversationMock.mockResolvedValueOnce(undefined);
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' },
			{ id: '22222222-2222-2222-2222-222222222222', title: 'Second chat' }
		]);
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '22222222-2222-2222-2222-222222222222' },
			url: { pathname: '/chat/22222222-2222-2222-2222-222222222222' }
		});
		render(Sidebar, { props: {} });
		const deleteButtons = screen.getAllByRole('button', { name: /delete conversation/i });
		await fireEvent.click(deleteButtons[0]);
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

		expect(removeConversationMock).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');
		expect(gotoMock).not.toHaveBeenCalled();
	});

	it('shows an unobtrusive hint when the conversation list failed to refresh', () => {
		(conversationsRefreshError as unknown as { set: (v: boolean) => void }).set(true);
		render(Sidebar, { props: {} });
		expect(screen.getByText(/couldn't refresh conversations/i)).toBeInTheDocument();
	});

	it('opens a rename modal prefilled with the current title and saves on submit', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		renameConversationMock.mockResolvedValueOnce(undefined);
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' }
		]);
		render(Sidebar, { props: {} });
		await fireEvent.click(screen.getByRole('button', { name: /rename conversation/i }));

		const dialog = await screen.findByRole('dialog', { name: 'Rename conversation' });
		const input = screen.getByLabelText('Title') as HTMLInputElement;
		expect(input.value).toBe('First chat');

		await fireEvent.input(input, { target: { value: 'Renamed chat' } });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

		expect(renameConversationMock).toHaveBeenCalledWith(
			'11111111-1111-1111-1111-111111111111',
			'Renamed chat'
		);
	});

	it('surfaces the rename error message instead of failing silently', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		renameConversationMock.mockRejectedValueOnce(new Error('title too long'));
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat' }
		]);
		render(Sidebar, { props: {} });
		await fireEvent.click(screen.getByRole('button', { name: /rename conversation/i }));
		const dialog = await screen.findByRole('dialog', { name: 'Rename conversation' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

		expect(await screen.findByText('title too long')).toBeInTheDocument();
	});
});
