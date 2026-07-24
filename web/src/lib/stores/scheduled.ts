import { writable } from 'svelte/store';
import { listScheduledTasks, type ScheduledTask } from '$lib/api/scheduled';
import { currentUser } from '$lib/stores/auth';

export type ScheduledTaskSummary = ScheduledTask;

export const scheduledTasks = writable<ScheduledTaskSummary[]>([]);
export const scheduledUnreadCount = writable(0);
export const scheduledRefreshError = writable(false);
export const scheduledHasMore = writable(false);
export const scheduledLoadingMore = writable(false);
let refreshGeneration = 0;
let initializedIdentity = false;
let activeUserID: number | null = null;
let nextOffset = 0;
let hasMore = false;
let loadingMore = false;

function setPagination(nextHasMore: boolean, offset: number): void {
	hasMore = nextHasMore;
	nextOffset = nextHasMore ? offset : 0;
	scheduledHasMore.set(nextHasMore);
}

function stopLoadingMore(): void {
	loadingMore = false;
	scheduledLoadingMore.set(false);
}

export async function refreshScheduled(): Promise<void> {
	const generation = ++refreshGeneration;
	stopLoadingMore();
	setPagination(false, 0);
	try {
		const snapshot = await listScheduledTasks(0);
		if (generation !== refreshGeneration) return;
		scheduledTasks.set(snapshot.tasks);
		scheduledUnreadCount.set(snapshot.unreadCount);
		setPagination(snapshot.hasMore, snapshot.nextOffset);
		scheduledRefreshError.set(false);
	} catch {
		if (generation !== refreshGeneration) return;
		scheduledRefreshError.set(true);
	}
}

export async function loadMoreScheduled(): Promise<void> {
	if (!hasMore || loadingMore) return;
	const generation = refreshGeneration;
	const offset = nextOffset;
	loadingMore = true;
	scheduledLoadingMore.set(true);
	try {
		const snapshot = await listScheduledTasks(offset);
		if (generation !== refreshGeneration) return;
		scheduledTasks.update((existing) => {
			const incoming = new Map(snapshot.tasks.map((task) => [task.id, task]));
			const merged = existing.map((task) => incoming.get(task.id) ?? task);
			const existingIDs = new Set(existing.map((task) => task.id));
			return [...merged, ...snapshot.tasks.filter((task) => !existingIDs.has(task.id))];
		});
		scheduledUnreadCount.set(snapshot.unreadCount);
		setPagination(snapshot.hasMore, snapshot.nextOffset);
		scheduledRefreshError.set(false);
	} catch {
		if (generation !== refreshGeneration) return;
		scheduledRefreshError.set(true);
	} finally {
		if (generation === refreshGeneration) stopLoadingMore();
	}
}

export function resetScheduled(): void {
	refreshGeneration += 1;
	stopLoadingMore();
	setPagination(false, 0);
	scheduledTasks.set([]);
	scheduledUnreadCount.set(0);
	scheduledRefreshError.set(false);
}

currentUser.subscribe((user) => {
	const userID = user?.id ?? null;
	if (initializedIdentity && userID !== activeUserID) resetScheduled();
	activeUserID = userID;
	initializedIdentity = true;
});
