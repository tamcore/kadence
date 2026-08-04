import { render, screen, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const newChatMock = vi.fn();
const removeConversationMock = vi.fn();
const renameConversationMock = vi.fn();
const pinConversationMock = vi.fn();
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
		renameConversation: (...a: unknown[]) => renameConversationMock(...a),
		pinConversation: (...a: unknown[]) => pinConversationMock(...a)
	};
});

vi.mock('$lib/stores/scheduled', async () => {
	const { writable } = await import('svelte/store');
	return {
		scheduledUnreadCount: writable(0),
		refreshScheduled: vi.fn().mockResolvedValue(undefined)
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
import { currentUser } from '$lib/stores/auth';
import { scheduledUnreadCount } from '$lib/stores/scheduled';
import { page } from '$app/stores';

afterEach(() => {
	vi.clearAllMocks();
	window.localStorage.clear();
	(conversations as unknown as { set: (v: unknown[]) => void }).set([]);
	(conversationsRefreshError as unknown as { set: (v: boolean) => void }).set(false);
	(page as unknown as { set: (v: unknown) => void }).set({
		params: { id: undefined },
		url: { pathname: '/chat' }
	});
	(currentUser as unknown as { set: (v: unknown) => void }).set({
		username: 'alice',
		role: 'member',
		scheduledEnabled: false
	});
	(scheduledUnreadCount as unknown as { set: (v: number) => void }).set(0);
});

describe('Sidebar', () => {
	async function openConversationMenu(title: string): Promise<void> {
		const { fireEvent } = await import('@testing-library/svelte');
		await fireEvent.click(screen.getByRole('button', { name: `${title} actions` }));
	}
	it('shows decorative artwork without changing the Kadence link name', () => {
		render(Sidebar, { props: {} });

		const brand = screen.getByRole('link', { name: 'Kadence' });
		const icon = brand.querySelector('img');
		expect(icon).toHaveAttribute('alt', '');
		expect(icon).toHaveAttribute('width', '24');
		expect(icon).toHaveAttribute('height', '24');
	});

	it('shows Scheduled only when enabled and marks descendant routes active', () => {
		(currentUser as unknown as { set: (v: unknown) => void }).set({
			username: 'alice',
			role: 'member',
			scheduledEnabled: true
		});
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: 'task-1' },
			url: { pathname: '/scheduled/task-1' }
		});
		render(Sidebar, { props: {} });

		const link = screen.getByRole('link', { name: 'Scheduled' });
		expect(link).toHaveClass('active');
		expect(link).toHaveAttribute('aria-current', 'page');
	});

	it('shows unread Scheduled activity and hides the entry when disabled', () => {
		(currentUser as unknown as { set: (v: unknown) => void }).set({
			username: 'alice',
			role: 'member',
			scheduledEnabled: true
		});
		(scheduledUnreadCount as unknown as { set: (v: number) => void }).set(4);
		const { unmount } = render(Sidebar, { props: {} });
		expect(screen.getByLabelText('4 unread scheduled results')).toBeInTheDocument();
		unmount();

		(currentUser as unknown as { set: (v: unknown) => void }).set({
			username: 'alice',
			role: 'member',
			scheduledEnabled: false
		});
		render(Sidebar, { props: {} });
		expect(screen.queryByRole('link', { name: /scheduled/i })).not.toBeInTheDocument();
	});

	it('keeps the header and account footer fixed around one central sidebar scroller', () => {
		const { container } = render(Sidebar, { props: {} });
		expect(container.querySelector('.sidebar-header')).toBeInTheDocument();
		expect(container.querySelector('.sidebar-scroll')).toBeInTheDocument();
		expect(container.querySelector('.sidebar-footer')).toBeInTheDocument();
	});

	it('shows empty state text when there are no conversations', () => {
		render(Sidebar, { props: {} });
		expect(screen.getByText(/no conversations yet/i)).toBeInTheDocument();
	});

	it('omits the entire Pinned section when every conversation is recent', () => {
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		expect(screen.queryByRole('button', { name: 'Pinned' })).not.toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Recents' })).toBeInTheDocument();
	});

	it('renders conversation titles', () => {
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: '2026-07-26T09:01:00Z' },
			{ id: '22222222-2222-2222-2222-222222222222', title: 'Second chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		expect(screen.getByText('First chat')).toBeInTheDocument();
		expect(screen.getByText('Second chat')).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Pinned' })).toHaveAttribute('aria-expanded', 'true');
		expect(screen.getByRole('button', { name: 'Recents' })).toHaveAttribute('aria-expanded', 'true');
	});

	it('marks the conversation matching the current route as active', () => {
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null },
			{ id: '22222222-2222-2222-2222-222222222222', title: 'Second chat', pinnedAt: null }
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
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		await openConversationMenu('First chat');
		await fireEvent.click(screen.getByRole('menuitem', { name: /delete/i, hidden: true }));
		expect(await screen.findByRole('dialog', { name: 'Delete conversation' })).toBeInTheDocument();

		await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		expect(removeConversationMock).not.toHaveBeenCalled();
		expect(screen.getByText('First chat')).toBeInTheDocument();
	});

	it('navigates home and closes the drawer when deleting the active conversation', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		removeConversationMock.mockResolvedValueOnce(undefined);
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '11111111-1111-1111-1111-111111111111' },
			url: { pathname: '/chat/11111111-1111-1111-1111-111111111111' }
		});
		render(Sidebar, { props: {} });
		await openConversationMenu('First chat');
		await fireEvent.click(screen.getByRole('menuitem', { name: /delete/i, hidden: true }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));

		expect(removeConversationMock).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111');
		expect(gotoMock).toHaveBeenCalledWith('/');
		expect(closeSidebarMock).toHaveBeenCalled();
	});

	it('does not navigate when deleting a non-active conversation', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		removeConversationMock.mockResolvedValueOnce(undefined);
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null },
			{ id: '22222222-2222-2222-2222-222222222222', title: 'Second chat', pinnedAt: null }
		]);
		(page as unknown as { set: (v: unknown) => void }).set({
			params: { id: '22222222-2222-2222-2222-222222222222' },
			url: { pathname: '/chat/22222222-2222-2222-2222-222222222222' }
		});
		render(Sidebar, { props: {} });
		await openConversationMenu('First chat');
		await fireEvent.click(screen.getByRole('menuitem', { name: /delete/i, hidden: true }));
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
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		await openConversationMenu('First chat');
		await fireEvent.click(screen.getByRole('menuitem', { name: /rename/i, hidden: true }));

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
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		await openConversationMenu('First chat');
		await fireEvent.click(screen.getByRole('menuitem', { name: /rename/i, hidden: true }));
		const dialog = await screen.findByRole('dialog', { name: 'Rename conversation' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

		expect(await screen.findByText('title too long')).toBeInTheDocument();
	});

	it('pins through the store and reports a failed action without changing the row', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		pinConversationMock.mockRejectedValueOnce(new Error('network down'));
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });

		const pin = screen.getByRole('button', { name: 'Pin conversation' });
		expect(pin).toHaveClass('pin-action');
		await fireEvent.click(pin);

		expect(pinConversationMock).toHaveBeenCalledWith('11111111-1111-1111-1111-111111111111', true);
		expect(await screen.findByRole('status')).toHaveTextContent('network down');
	});

	it('remembers expanded conversation sections with the stable local-storage keys', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		const toggle = screen.getByRole('button', { name: 'Recents' });
		await fireEvent.click(toggle);
		expect(toggle).toHaveAttribute('aria-expanded', 'false');
		expect(window.localStorage.getItem('kadence_sidebar_recents_expanded')).toBe('false');
	});

	it('orders the overflow actions and marks unavailable actions as coming soon', async () => {
		const { fireEvent } = await import('@testing-library/svelte');
		(conversations as unknown as { set: (v: unknown[]) => void }).set([
			{ id: '11111111-1111-1111-1111-111111111111', title: 'First chat', pinnedAt: null }
		]);
		render(Sidebar, { props: {} });
		await openConversationMenu('First chat');

		expect(screen.getAllByRole('menuitem', { hidden: true }).map((item) => item.getAttribute('aria-label') ?? item.textContent)).toEqual([
			'Share (coming soon)',
			'Rename',
			'Pin',
			'Archive (coming soon)',
			'Delete'
		]);
		expect(screen.getAllByRole('separator', { hidden: true })).toHaveLength(2);
	});

	it('puts a theme control in the footer beside the account menu', () => {
		const { container } = render(Sidebar, { props: {} });
		const footer = container.querySelector('.sidebar-footer');
		expect(footer).toBeInTheDocument();
		expect(
			within(footer as HTMLElement).getByRole('button', { name: /^Switch theme to / })
		).toBeInTheDocument();
		expect(
			within(footer as HTMLElement).getByRole('button', { name: 'Account actions' })
		).toBeInTheDocument();
	});

	it('does not add menu items or separators to the document', () => {
		render(Sidebar, { props: {} });
		expect(screen.queryAllByRole('menuitem', { hidden: true })).toHaveLength(0);
		expect(screen.queryAllByRole('separator', { hidden: true })).toHaveLength(0);
	});
});
