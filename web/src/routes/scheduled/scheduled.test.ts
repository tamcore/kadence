import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';

const gotoMock = vi.fn();
const confirmMock = vi.fn();
const streamMock = vi.fn();
const refreshMock = vi.fn();
const loadMoreMock = vi.fn();
const detailMock = vi.fn();

vi.mock('$app/navigation', () => ({ goto: (...args: unknown[]) => gotoMock(...args) }));
vi.mock('$app/stores', async () => {
	const { writable } = await import('svelte/store');
	return { page: writable({ params: {}, url: new URL('http://localhost/scheduled') }) };
});
vi.mock('$lib/api/scheduled', async (importOriginal) => {
	const original = await importOriginal<typeof import('$lib/api/scheduled')>();
	return {
		...original,
		streamScheduledDefinition: (...args: unknown[]) => streamMock(...args),
		confirmScheduledTask: (...args: unknown[]) => confirmMock(...args),
		getScheduledTask: (...args: unknown[]) => detailMock(...args)
	};
});
vi.mock('$lib/stores/scheduled', async () => {
	const { writable } = await import('svelte/store');
	return {
		scheduledTasks: writable([]),
		scheduledUnreadCount: writable(0),
		scheduledHasMore: writable(false),
		scheduledLoadingMore: writable(false),
		scheduledRefreshError: writable(false),
		refreshScheduled: (...args: unknown[]) => refreshMock(...args),
		loadMoreScheduled: (...args: unknown[]) => loadMoreMock(...args)
	};
});

import Page from './+page.svelte';
import {
	scheduledHasMore,
	scheduledLoadingMore,
	scheduledTasks
} from '$lib/stores/scheduled';
import { page } from '$app/stores';

async function* events(items: unknown[]) {
	for (const item of items) yield item;
}

afterEach(() => {
	vi.clearAllMocks();
	sessionStorage.clear();
	(scheduledTasks as unknown as { set: (value: unknown[]) => void }).set([]);
	(scheduledHasMore as unknown as { set: (value: boolean) => void }).set(false);
	(scheduledLoadingMore as unknown as { set: (value: boolean) => void }).set(false);
	refreshMock.mockResolvedValue(undefined);
	loadMoreMock.mockResolvedValue(undefined);
	(page as unknown as { set: (value: unknown) => void }).set({
		params: {},
		url: new URL('http://localhost/scheduled')
	});
});

describe('/scheduled', () => {
	it('shows a calm landing composer and genuine coach examples', () => {
		render(Page);
		expect(screen.getByRole('heading', { name: 'Scheduled' })).toBeInTheDocument();
		expect(screen.getByPlaceholderText('Describe what should happen later…')).toBeInTheDocument();
		expect(screen.getByText(/feedback after my next run/i)).toBeInTheDocument();
	});

	it('refines one question at a time and confirms the versioned proposal', async () => {
		streamMock
			.mockImplementationOnce(() =>
				events([
					{ type: 'meta', taskId: 'task-1', conversationId: 'conv-1' },
					{ type: 'text', delta: 'Let’s choose a cadence.' },
					{
						type: 'task_question',
						question: {
							id: 'cadence',
							prompt: 'How often?',
							kind: 'single_select',
							options: [{ label: 'Every day', value: 'daily' }],
							allowCustom: false,
							optional: false
						}
					},
					{ type: 'done' }
				])
			)
			.mockImplementationOnce(() =>
				events([
					{ type: 'text', delta: 'This is ready.' },
					{
						type: 'task_proposal',
						proposal: {
							version: 3,
							name: 'Daily run brief',
							taskKind: 'data',
							compiledPrompt: 'Review the latest run.',
							executionMode: 'data',
							schedule: { RRULE: 'FREQ=DAILY', Timezone: 'Europe/Berlin' },
							timezone: 'Europe/Berlin',
							authorizedTools: ['garmin__activities'],
							deliveryPolicy: 'always',
							initialRun: 'wait'
						}
					},
					{ type: 'done' }
				])
			);
		confirmMock.mockResolvedValue({ id: 'task-1', state: 'active' });
		render(Page);

		await fireEvent.input(screen.getByLabelText('Message'), {
			target: { value: 'Give me feedback after my runs' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Send' }));
		expect(await screen.findByText('Let’s choose a cadence.')).toBeInTheDocument();
		expect(sessionStorage.getItem('kadence_scheduled_draft:anonymous:task-1')).toContain(
			'"kind":"single_select"'
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Every day' }));
		expect(await screen.findByRole('heading', { name: 'Daily run brief' })).toBeInTheDocument();
		expect(screen.getByText('Let’s choose a cadence.')).toBeInTheDocument();
		expect(streamMock).toHaveBeenLastCalledWith(
			{ taskId: 'task-1', message: 'daily' },
			expect.any(AbortSignal)
		);

		await fireEvent.click(screen.getByRole('button', { name: 'Schedule task' }));
		await waitFor(() => expect(confirmMock).toHaveBeenCalledWith('task-1', 3));
		expect(gotoMock).toHaveBeenCalledWith('/scheduled/task-1');
	});

	it('resumes a persisted draft and restores its full conversation transcript', async () => {
		(page as unknown as { set: (value: unknown) => void }).set({
			params: {},
			url: new URL('http://localhost/scheduled?task=task-draft')
		});
		detailMock.mockResolvedValue({
			task: {
				id: 'task-draft',
				conversationId: 'conv-draft',
				state: 'draft',
				version: 2,
				compiledPrompt: ''
			},
			runs: [],
			definitionMessages: [
				{ role: 'user', text: 'Review my next run' },
				{
					role: 'assistant',
					text: 'Which details should I focus on?'
				},
				{ role: 'user', text: 'Pace and recovery' },
				{
					role: 'assistant',
					text: 'Choose a cadence.',
					question: {
					id: 'cadence',
					prompt: 'How often should I check?',
					kind: 'single_select',
					options: [{ label: 'Daily', value: 'daily' }],
					allowCustom: false,
					optional: false
				}
				}
			]
		});

		render(Page);

		expect(await screen.findByText('Which details should I focus on?')).toBeInTheDocument();
		expect(screen.getByText('Pace and recovery')).toBeInTheDocument();
		expect(screen.getByRole('heading', { name: 'How often should I check?' })).toBeInTheDocument();
	});

	it('Back restores the previous structured question and its persisted answer', async () => {
		(page as unknown as { set: (value: unknown) => void }).set({
			params: {},
			url: new URL('http://localhost/scheduled?task=task-draft')
		});
		detailMock.mockResolvedValue({
			task: {
				id: 'task-draft',
				conversationId: 'conv-draft',
				state: 'draft',
				version: 3,
				compiledPrompt: ''
			},
			runs: [],
			definitionMessages: [
				{ role: 'user', text: 'Review my next run' },
				{
					role: 'assistant',
					text: 'Choose a cadence.',
					question: {
						id: 'cadence',
						prompt: 'How often should I check?',
						kind: 'single_select',
						options: [{ label: 'Daily', value: 'daily' }],
						allowCustom: false,
						optional: false
					}
				},
				{ role: 'user', text: 'daily' },
				{
					role: 'assistant',
					text: 'Choose a focus.',
					question: {
						id: 'focus',
						prompt: 'What should I focus on?',
						kind: 'text',
						allowCustom: true,
						optional: false
					}
				},
				{ role: 'user', text: 'Pace and recovery' },
				{
					role: 'assistant',
					text: 'Choose the delivery.',
					question: {
						id: 'delivery',
						prompt: 'When should I notify you?',
						kind: 'single_select',
						options: [{ label: 'Every time', value: 'always' }],
						allowCustom: false,
						optional: false
					}
				}
			]
		});

		render(Page);

		expect(await screen.findByRole('heading', { name: 'When should I notify you?' })).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));

		expect(screen.getByRole('heading', { name: 'What should I focus on?' })).toBeInTheDocument();
		expect(screen.getByLabelText('Your answer')).toHaveValue('Pace and recovery');
	});

	it('lists existing tasks with status and next run', () => {
		(scheduledTasks as unknown as { set: (value: unknown[]) => void }).set([
			{
				id: 'task-2',
				name: 'Recovery check',
				state: 'paused',
				nextRunAt: '2026-07-25T06:00:00Z',
				timezone: 'Europe/Berlin',
				unreadCount: 2,
				recentRun: { id: 9, state: 'no_change', unread: false }
			}
		]);
		render(Page);
		expect(screen.getByRole('link', { name: /Recovery check/ })).toHaveAttribute(
			'href',
			'/scheduled/task-2'
		);
		expect(screen.getByText('Paused')).toBeInTheDocument();
		expect(screen.getByLabelText('2 unread results')).toBeInTheDocument();
		expect(screen.getByText(/latest: no change/i)).toBeInTheDocument();
	});

	it.each(['pending', 'running'])(
		'shows that task controls are unavailable while the latest occurrence is %s',
		(state) => {
			(scheduledTasks as unknown as { set: (value: unknown[]) => void }).set([
				{
					id: 'task-in-progress',
					name: 'Active recovery check',
					state: 'active',
					timezone: 'UTC',
					unreadCount: 0,
					recentRun: { id: 10, state, unread: false }
				}
			]);
			render(Page);

			expect(screen.getByText(new RegExp(`latest: ${state}`, 'i'))).toHaveTextContent(
				'Task controls unavailable until this run finishes'
			);
		}
	);

	it('offers an accessible continuation control when more tasks are available', async () => {
		(scheduledTasks as unknown as { set: (value: unknown[]) => void }).set([
			{
				id: 'task-1',
				name: 'First page task',
				state: 'active',
				timezone: 'UTC',
				unreadCount: 0
			}
		]);
		(scheduledHasMore as unknown as { set: (value: boolean) => void }).set(true);
		render(Page);

		const button = screen.getByRole('button', { name: 'Load more tasks' });
		await fireEvent.click(button);
		expect(loadMoreMock).toHaveBeenCalledOnce();

		(scheduledLoadingMore as unknown as { set: (value: boolean) => void }).set(true);
		const loading = await screen.findByRole('button', { name: 'Loading more tasks' });
		expect(loading).toBeDisabled();
		expect(loading).toHaveAttribute(
			'aria-busy',
			'true'
		);
	});
});
