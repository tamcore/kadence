import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const getMock = vi.fn();
const pauseMock = vi.fn();
const resumeMock = vi.fn();
const runMock = vi.fn();
const readMock = vi.fn();
const deleteMock = vi.fn();
const gotoMock = vi.fn();

vi.mock('$app/navigation', () => ({ goto: (...args: unknown[]) => gotoMock(...args) }));
vi.mock('$app/stores', async () => {
	const { writable } = await import('svelte/store');
	return { page: writable({ params: { id: 'task-1' }, url: { pathname: '/scheduled/task-1' } }) };
});
vi.mock('$lib/api/scheduled', () => ({
	getScheduledTask: (...args: unknown[]) => getMock(...args),
	pauseScheduledTask: (...args: unknown[]) => pauseMock(...args),
	resumeScheduledTask: (...args: unknown[]) => resumeMock(...args),
	runScheduledTaskNow: (...args: unknown[]) => runMock(...args),
	markScheduledTaskRead: (...args: unknown[]) => readMock(...args),
	deleteScheduledTask: (...args: unknown[]) => deleteMock(...args)
}));
vi.mock('$lib/stores/scheduled', () => ({ refreshScheduled: vi.fn() }));
vi.mock('$lib/stores/auth', async () => {
	const { writable } = await import('svelte/store');
	return { currentUser: writable({ scheduledEnabled: true }) };
});

import Page from './+page.svelte';
import { currentUser } from '$lib/stores/auth';

const detail = {
	task: {
		id: 'task-1',
		name: 'Post-run review',
		state: 'active',
		nextRunAt: '2026-07-25T08:00:00Z',
		timezone: 'Europe/Berlin',
		deliveryPolicy: 'always',
		authorizedTools: ['garmin__activities']
	},
	runs: [
		{
			id: 2,
			state: 'delivered',
			scheduledFor: '2026-07-24T08:00:00Z',
			result: 'Your pace was steady.',
			unread: true
		},
		{
			id: 1,
			state: 'failed',
			scheduledFor: '2026-07-23T08:00:00Z',
			error: 'provider_timeout',
			unread: false
		}
	]
};

afterEach(() => {
	vi.clearAllMocks();
	(currentUser as unknown as { set: (value: unknown) => void }).set({ scheduledEnabled: true });
});

describe('/scheduled/[id]', () => {
	it('shows state, next run, delivered history, errors, and marks unread results read', async () => {
		const zoned = structuredClone(detail);
		zoned.task.timezone = 'America/Los_Angeles';
		getMock.mockResolvedValue(zoned);
		readMock.mockResolvedValue({ ok: true });
		render(Page);

		expect(await screen.findByRole('heading', { name: 'Post-run review' })).toBeInTheDocument();
		expect(screen.getByText('Your pace was steady.')).toBeInTheDocument();
		expect(screen.getByText(/provider timeout/i)).toBeInTheDocument();
		expect(screen.getAllByText(/1:00:00 AM/).length).toBeGreaterThan(0);
		await waitFor(() => expect(readMock).toHaveBeenCalledWith('task-1'));
	});

	it('retries transient polling failures and stops polling after unmount', async () => {
		vi.useFakeTimers();
		const pending = structuredClone(detail);
		pending.runs = [
			{
				id: 3,
				state: 'pending',
				scheduledFor: '2026-07-25T08:00:00Z',
				error: '',
				unread: false
			}
		];
		const delivered = structuredClone(detail);
		getMock
			.mockResolvedValueOnce(pending)
			.mockRejectedValueOnce(new Error('temporary'))
			.mockResolvedValueOnce(delivered);
		const view = render(Page);
		await vi.waitFor(() => expect(screen.getByRole('heading', { name: 'Post-run review' })).toBeInTheDocument());
		await vi.advanceTimersByTimeAsync(5000);
		expect(getMock).toHaveBeenCalledTimes(2);
		await vi.advanceTimersByTimeAsync(5000);
		expect(getMock).toHaveBeenCalledTimes(3);
		view.unmount();
		await vi.advanceTimersByTimeAsync(5000);
		expect(getMock).toHaveBeenCalledTimes(3);
		vi.useRealTimers();
	});

	it('keeps polling a pending unread run when marking it read fails', async () => {
		vi.useFakeTimers();
		const pending = structuredClone(detail);
		pending.runs = [
			{
				id: 3,
				state: 'pending',
				scheduledFor: '2026-07-25T08:00:00Z',
				error: '',
				unread: true
			}
		];
		getMock.mockResolvedValue(pending);
		readMock.mockRejectedValue(new Error('Could not mark results read'));
		const view = render(Page);

		await vi.waitFor(() =>
			expect(screen.getByRole('alert')).toHaveTextContent('Could not mark results read')
		);
		await vi.advanceTimersByTimeAsync(5000);
		expect(getMock).toHaveBeenCalledTimes(2);

		view.unmount();
		vi.useRealTimers();
	});

	it('supports pause, run now, and confirmed deletion', async () => {
		getMock.mockResolvedValue(structuredClone(detail));
		pauseMock.mockResolvedValue({ ...detail.task, state: 'paused' });
		runMock.mockResolvedValue({
			id: 3,
			state: 'pending',
			scheduledFor: '2026-07-25T08:00:00Z',
			unread: false
		});
		deleteMock.mockResolvedValue({ ok: true });
		render(Page);
		await screen.findByRole('heading', { name: 'Post-run review' });

		await fireEvent.click(screen.getByRole('button', { name: 'Pause' }));
		await waitFor(() => expect(pauseMock).toHaveBeenCalledWith('task-1'));
		await fireEvent.click(screen.getByRole('button', { name: 'Run now' }));
		expect(runMock).toHaveBeenCalledWith('task-1');
		await fireEvent.click(screen.getByRole('button', { name: 'Delete task' }));
		const dialog = screen.getByRole('dialog', { name: 'Delete scheduled task' });
		await fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
		await waitFor(() => expect(deleteMock).toHaveBeenCalledWith('task-1'));
		expect(gotoMock).toHaveBeenCalledWith('/scheduled');
	});

	it('redirects without loading when Scheduled is disabled', async () => {
		(currentUser as unknown as { set: (value: unknown) => void }).set({ scheduledEnabled: false });
		render(Page);
		await waitFor(() => expect(gotoMock).toHaveBeenCalledWith('/'));
		expect(getMock).not.toHaveBeenCalled();
	});
});
