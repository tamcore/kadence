import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';

const listMock = vi.fn();
vi.mock('$lib/api/scheduled', () => ({
	listScheduledTasks: (...args: unknown[]) => listMock(...args)
}));

import {
	loadMoreScheduled,
	refreshScheduled,
	resetScheduled,
	scheduledHasMore,
	scheduledLoadingMore,
	scheduledRefreshError,
	scheduledTasks,
	scheduledUnreadCount
} from './scheduled';
import { clearAuth, setAuth } from './auth';

function user(id: number, username: string) {
	return {
		id,
		username,
		email: `${username}@example.test`,
		role: 'user' as const,
		displayName: username,
		unitSystem: 'metric' as const,
		location: '',
		aboutMe: '',
		timezone: 'UTC',
		scheduledEnabled: true
	};
}

afterEach(() => {
	vi.clearAllMocks();
	clearAuth();
	resetScheduled();
});

describe('Scheduled store', () => {
	it('refreshes the task list and unread count together', async () => {
		listMock.mockResolvedValue({
			tasks: [{ id: 'task-1', name: 'Morning brief', state: 'active' }],
			unreadCount: 3,
			hasMore: true,
			nextOffset: 1
		});

		await refreshScheduled();

		expect(listMock).toHaveBeenCalledWith(0);
		expect(get(scheduledTasks)).toEqual([
			expect.objectContaining({ id: 'task-1', name: 'Morning brief' })
		]);
		expect(get(scheduledUnreadCount)).toBe(3);
		expect(get(scheduledHasMore)).toBe(true);
		expect(get(scheduledRefreshError)).toBe(false);
	});

	it('enriches task rows with unread activity and the most recent outcome', async () => {
		listMock.mockResolvedValue({
			tasks: [
				{
					id: 'task-1',
					name: 'Morning brief',
					state: 'active',
					unreadCount: 1,
					recentRun: { id: 2, state: 'delivered', unread: true, result: 'A new brief' }
				}
			],
			unreadCount: 1,
			hasMore: false,
			nextOffset: 0
		});

		await refreshScheduled();

		expect(get(scheduledTasks)[0]).toEqual(
			expect.objectContaining({
				unreadCount: 1,
				recentRun: expect.objectContaining({ id: 2, state: 'delivered' })
			})
		);
	});

	it('keeps the last snapshot and exposes refresh failure', async () => {
		scheduledTasks.set([{ id: 'old' } as never]);
		scheduledUnreadCount.set(2);
		listMock.mockRejectedValue(new Error('offline'));

		await refreshScheduled();

		expect(get(scheduledTasks)).toEqual([{ id: 'old' }]);
		expect(get(scheduledUnreadCount)).toBe(2);
		expect(get(scheduledRefreshError)).toBe(true);
	});

	it('uses the bounded list snapshot without per-task detail requests', async () => {
		listMock.mockResolvedValue({
			tasks: Array.from({ length: 12 }, (_, index) => ({ id: `task-${index}` })),
			unreadCount: 0,
			hasMore: false,
			nextOffset: 0
		});

		await refreshScheduled();

		expect(get(scheduledTasks)).toHaveLength(12);
	});

	it('loads the next offset and appends only new task IDs', async () => {
		listMock
			.mockResolvedValueOnce({
				tasks: [{ id: 'task-1' }, { id: 'task-2', name: 'Older name' }],
				unreadCount: 2,
				hasMore: true,
				nextOffset: 2
			})
			.mockResolvedValueOnce({
				tasks: [{ id: 'task-2', name: 'Current name' }, { id: 'task-3' }],
				unreadCount: 3,
				hasMore: false,
				nextOffset: 0
			});
		await refreshScheduled();

		await loadMoreScheduled();

		expect(listMock).toHaveBeenNthCalledWith(2, 2);
		expect(get(scheduledTasks).map((task) => task.id)).toEqual([
			'task-1',
			'task-2',
			'task-3'
		]);
		expect(get(scheduledTasks)[1]).toEqual(expect.objectContaining({ name: 'Current name' }));
		expect(get(scheduledUnreadCount)).toBe(3);
		expect(get(scheduledHasMore)).toBe(false);
		expect(get(scheduledLoadingMore)).toBe(false);
	});

	it('ignores a late load-more response after the authenticated identity changes', async () => {
		setAuth(user(1, 'alice'));
		listMock.mockResolvedValueOnce({
			tasks: [{ id: 'alice-task-1' }],
			unreadCount: 0,
			hasMore: true,
			nextOffset: 1
		});
		await refreshScheduled();
		let resolveMore!: (value: unknown) => void;
		listMock.mockReturnValueOnce(
			new Promise((resolve) => {
				resolveMore = resolve;
			})
		);
		const loading = loadMoreScheduled();

		setAuth(user(2, 'bob'));
		resolveMore({
			tasks: [{ id: 'alice-task-2' }],
			unreadCount: 1,
			hasMore: false,
			nextOffset: 0
		});
		await loading;

		expect(get(scheduledTasks)).toEqual([]);
		expect(get(scheduledUnreadCount)).toBe(0);
		expect(get(scheduledHasMore)).toBe(false);
		expect(get(scheduledLoadingMore)).toBe(false);
	});

	it('clears task metadata when the authenticated identity changes or becomes null', () => {
		setAuth(user(1, 'alice'));
		scheduledTasks.set([{ id: 'alice-task' } as never]);
		scheduledUnreadCount.set(4);
		scheduledRefreshError.set(true);

		setAuth(user(2, 'bob'));

		expect(get(scheduledTasks)).toEqual([]);
		expect(get(scheduledUnreadCount)).toBe(0);
		expect(get(scheduledRefreshError)).toBe(false);
		expect(get(scheduledHasMore)).toBe(false);
		expect(get(scheduledLoadingMore)).toBe(false);

		scheduledTasks.set([{ id: 'bob-task' } as never]);
		scheduledUnreadCount.set(2);
		clearAuth();
		expect(get(scheduledTasks)).toEqual([]);
		expect(get(scheduledUnreadCount)).toBe(0);
	});

	it('does not restore an old user snapshot when its refresh finishes after an identity switch', async () => {
		let resolveList!: (value: unknown) => void;
		listMock.mockReturnValue(
			new Promise((resolve) => {
				resolveList = resolve;
			})
		);
		setAuth(user(1, 'alice'));
		const refresh = refreshScheduled();

		setAuth(user(2, 'bob'));
		resolveList({
			tasks: [{ id: 'alice-task', name: 'Alice task' }],
			unreadCount: 7,
			hasMore: true,
			nextOffset: 1
		});
		await refresh;

		expect(get(scheduledTasks)).toEqual([]);
		expect(get(scheduledUnreadCount)).toBe(0);
		expect(get(scheduledRefreshError)).toBe(false);
	});
});
