import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { APIError } from '$lib/api/client';
import type { ScheduledArtifact } from '$lib/types';

const confirmMock = vi.fn();
const discardMock = vi.fn();
const getTaskMock = vi.fn();
const streamMock = vi.fn();

vi.mock('$lib/api/scheduled', async (importOriginal) => ({
	...(await importOriginal<typeof import('$lib/api/scheduled')>()),
	confirmScheduledTask: (...args: unknown[]) => confirmMock(...args),
	discardScheduledDraft: (...args: unknown[]) => discardMock(...args),
	getScheduledTask: (...args: unknown[]) => getTaskMock(...args),
	streamScheduledDefinition: (...args: unknown[]) => streamMock(...args)
}));

import ScheduledArtifactCard from './ScheduledArtifactCard.svelte';

const proposal = {
	version: 7,
	name: 'Post-run review',
	taskKind: 'data' as const,
	compiledPrompt: 'Review the latest run.',
	executionMode: 'data' as const,
	schedule: { RRULE: 'FREQ=DAILY', Timezone: 'UTC' },
	timezone: 'UTC',
	authorizedTools: [],
	deliveryPolicy: 'always' as const,
	initialRun: 'wait' as const
};

function artifact(overrides: Partial<ScheduledArtifact> = {}): ScheduledArtifact {
	return {
		handoffId: 'handoff-1',
		taskId: 'task-1',
		ordinal: 1,
		artifactState: 'ready',
		taskState: 'draft',
		version: 7,
		proposal,
		...overrides
	};
}

afterEach(() => vi.clearAllMocks());

describe('ScheduledArtifactCard', () => {
	it('renders each durable artifact state as delegated work rather than a generic tool chip', () => {
		const cases: Array<[ScheduledArtifact, RegExp]> = [
			[artifact({ artifactState: 'creating' }), /Preparing delegated task/i],
			[artifact({ taskState: 'active', proposal: undefined }), /Scheduled/i],
			[artifact({ taskState: 'paused', proposal: undefined }), /Paused/i],
			[artifact({ taskState: 'completed', proposal: undefined }), /Completed/i],
			[artifact({ taskState: 'failed', proposal: undefined }), /Failed/i],
			[artifact({ taskState: 'deleted', proposal: undefined }), /Deleted/i],
			[artifact({ artifactState: 'dismissed', proposal: undefined }), /Dismissed/i],
			[artifact({ artifactState: 'failed', retryable: true, proposal: undefined }), /Retry/i]
		];

		for (const [value, label] of cases) {
			const result = render(ScheduledArtifactCard, { props: { artifact: value } });
			expect(within(result.container).getByRole('status')).toHaveTextContent(label);
			result.unmount();
		}
	});

	it('renders stable categories for failed handoff diagnostics and keeps retry available', () => {
		const failures: Array<[string, string, boolean]> = [
			['provider_unavailable', 'Provider unavailable', true],
			['provider_timeout', 'Timeout', true],
			['invalid_definition', 'Invalid task definition', false],
			['internal_error', 'Internal error', true],
			['compiler_failed', 'This delegated task could not be prepared.', true]
		];
		for (const [errorCode, label, retryable] of failures) {
			const result = render(ScheduledArtifactCard, {
				props: {
					artifact: artifact({
						artifactState: 'failed',
						errorCode,
						retryable,
						proposal: undefined
					})
				}
			});

			expect(within(result.container).getByRole('alert')).toHaveTextContent(label);
			if (retryable) {
				expect(within(result.container).getByRole('button', { name: 'Retry' })).toBeInTheDocument();
			} else {
				expect(within(result.container).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
			}
			result.unmount();
		}
	});

	it('keeps question controls unfocused, proposes the exact version, and links to task detail', async () => {
		confirmMock.mockResolvedValueOnce({ id: 'task-1', state: 'active' });
		render(ScheduledArtifactCard, {
			props: {
				artifact: artifact({
					question: {
						id: 'focus',
						prompt: 'What should I watch?',
						kind: 'text',
						allowCustom: true,
						optional: false
					},
					proposal: undefined
				})
			}
		});
		expect(document.activeElement).not.toBe(screen.getByRole('textbox', { name: 'Your answer' }).closest('section'));

		const result = render(ScheduledArtifactCard, { props: { artifact: artifact() } });
		await fireEvent.click(screen.getByRole('button', { name: 'Schedule task' }));
		expect(confirmMock).toHaveBeenCalledWith('task-1', 7);
		expect(within(result.container).getByRole('link', { name: /scheduled details/i })).toHaveAttribute(
			'href',
			'/scheduled/task-1'
		);
		result.unmount();
	});

	it('replaces a hydrated question with the returned proposal after answering', async () => {
		streamMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-1', conversationId: 'conversation-1' };
			yield { type: 'task_proposal', proposal };
			yield { type: 'done' };
		});
		const result = render(ScheduledArtifactCard, {
			props: {
				artifact: artifact({
					question: {
						id: 'hydrated-question',
						prompt: 'What should I watch?',
						kind: 'text',
						allowCustom: true,
						optional: false
					},
					proposal: undefined
				})
			}
		});

		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Your answer' }), {
			target: { value: 'Heart rate' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Continue' }));

		await waitFor(() => expect(within(result.container).getByRole('button', { name: 'Schedule task' })).toBeInTheDocument());
		expect(within(result.container).queryByText('What should I watch?')).not.toBeInTheDocument();
	});

	it('restores a hydrated question when answering fails', async () => {
		streamMock.mockImplementation(async function* () {
			throw new Error('definition unavailable');
		});
		const result = render(ScheduledArtifactCard, {
			props: {
				artifact: artifact({
					question: {
						id: 'hydrated-question',
						prompt: 'What should I watch?',
						kind: 'text',
						allowCustom: true,
						optional: false
					},
					proposal: undefined
				})
			}
		});

		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Your answer' }), {
			target: { value: 'Heart rate' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Continue' }));

		await waitFor(() =>
			expect(within(result.container).getByRole('heading', { name: 'What should I watch?' })).toBeInTheDocument()
		);
		expect(within(result.container).getByText('definition unavailable')).toBeInTheDocument();
	});

	it('keeps the latest local question when its answer fails', async () => {
		streamMock
			.mockImplementationOnce(async function* () {
				yield {
					type: 'task_question',
					question: {
						id: 'local-question',
						prompt: 'Which metric matters most?',
						kind: 'text',
						allowCustom: true,
						optional: false
					}
				};
			})
			.mockImplementationOnce(async function* () {
				throw new Error('definition unavailable');
			});
		const result = render(ScheduledArtifactCard, {
			props: {
				artifact: artifact({
					question: {
						id: 'hydrated-question',
						prompt: 'What should I watch?',
						kind: 'text',
						allowCustom: true,
						optional: false
					},
					proposal: undefined
				})
			}
		});

		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Your answer' }), {
			target: { value: 'Heart rate' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Continue' }));
		await waitFor(() =>
			expect(
				within(result.container).getByRole('heading', { name: 'Which metric matters most?' })
			).toBeInTheDocument()
		);

		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Your answer' }), {
			target: { value: 'Recovery' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Continue' }));

		await waitFor(() =>
			expect(
				within(result.container).getByRole('heading', { name: 'Which metric matters most?' })
			).toBeInTheDocument()
		);
		expect(within(result.container).queryByText('What should I watch?')).not.toBeInTheDocument();
		expect(within(result.container).getByText('definition unavailable')).toBeInTheDocument();
	});

	it('restores a hydrated proposal when adjustment fails', async () => {
		streamMock.mockImplementation(async function* () {
			throw new Error('definition unavailable');
		});
		const result = render(ScheduledArtifactCard, {
			props: { artifact: artifact() }
		});

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Adjust' }));
		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Adjust scheduled task' }), {
			target: { value: 'Use a shorter summary.' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Save adjustment' }));

		await waitFor(() =>
			expect(within(result.container).getByRole('button', { name: 'Schedule task' })).toBeInTheDocument()
		);
		expect(within(result.container).getByText('definition unavailable')).toBeInTheDocument();
	});

	it('keeps the proposal returned by retry when its adjustment fails', async () => {
		streamMock
			.mockImplementationOnce(async function* () {
				yield { type: 'task_proposal', proposal };
			})
			.mockImplementationOnce(async function* () {
				throw new Error('definition unavailable');
			});
		const result = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ artifactState: 'failed', retryable: true, proposal: undefined }) }
		});

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Retry' }));
		await waitFor(() =>
			expect(within(result.container).getByRole('button', { name: 'Schedule task' })).toBeInTheDocument()
		);

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Adjust' }));
		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Adjust scheduled task' }), {
			target: { value: 'Use a shorter summary.' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Save adjustment' }));

		await waitFor(() =>
			expect(within(result.container).getByRole('button', { name: 'Schedule task' })).toBeInTheDocument()
		);
		expect(
			within(result.container).getByText('Ready to schedule', { selector: '.scheduled-state' })
		).toBeInTheDocument();
		expect(within(result.container).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
		expect(within(result.container).getByText('definition unavailable')).toBeInTheDocument();
	});

	it('does not offer draft actions for terminal tasks that retain a hydrated proposal', () => {
		for (const taskState of ['active', 'paused', 'completed', 'failed', 'deleted'] as const) {
			const result = render(ScheduledArtifactCard, {
				props: { artifact: artifact({ taskState }) }
			});

			expect(within(result.container).queryByRole('button', { name: 'Schedule task' })).not.toBeInTheDocument();
			if (taskState === 'deleted') {
				expect(within(result.container).queryByRole('link', { name: /scheduled details/i })).not.toBeInTheDocument();
			}
			result.unmount();
		}
	});

	it('does not offer retry for a failed non-draft task', () => {
		const result = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ artifactState: 'failed', retryable: true, taskState: 'failed' }) }
		});

		expect(within(result.container).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
	});

	it('marks a failed artifact ready when retry returns a proposal', async () => {
		streamMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-1', conversationId: 'conversation-1' };
			yield { type: 'task_proposal', proposal };
			yield { type: 'done' };
		});
		const result = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ artifactState: 'failed', retryable: true, proposal: undefined }) }
		});

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Retry' }));

		await waitFor(() =>
			expect(within(result.container).getByRole('status')).toHaveTextContent('Ready to schedule')
		);
		expect(within(result.container).queryByRole('alert')).not.toBeInTheDocument();
		expect(within(result.container).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
	});

	it('marks a failed artifact ready when retry returns a question', async () => {
		streamMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-1', conversationId: 'conversation-1' };
			yield {
				type: 'task_question',
				question: {
					id: 'retry-question',
					prompt: 'Which forecast source should I use?',
					kind: 'text',
					allowCustom: true,
					optional: false
				}
			};
			yield { type: 'done' };
		});
		const result = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ artifactState: 'failed', retryable: true, proposal: undefined }) }
		});

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Retry' }));

		await waitFor(() =>
			expect(within(result.container).getByRole('status')).toHaveTextContent('Needs your input')
		);
		expect(within(result.container).getByRole('heading', { name: 'Which forecast source should I use?' })).toBeInTheDocument();
		expect(within(result.container).queryByRole('alert')).not.toBeInTheDocument();
		expect(within(result.container).queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
	});

	it('uses a per-card pending state and disables controls for an unpersisted assistant', async () => {
		streamMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-1', conversationId: 'conversation-1' };
			await new Promise((resolve) => setTimeout(resolve, 20));
			yield { type: 'done' };
		});
		render(ScheduledArtifactCard, { props: { artifact: artifact(), disabled: true } });
		expect(screen.getByRole('button', { name: 'Schedule task' })).toBeDisabled();

		const first = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ handoffId: 'handoff-a', taskId: 'task-a', proposal: undefined }) }
		});
		const second = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ handoffId: 'handoff-b', taskId: 'task-b', proposal: undefined }) }
		});
		await fireEvent.click(within(first.container).getByRole('button', { name: 'Adjust' }));
		expect(within(first.container).getByRole('textbox', { name: 'Adjust scheduled task' })).not.toBeDisabled();
		expect(within(second.container).getByRole('button', { name: 'Adjust' })).not.toBeDisabled();
		first.unmount();
		second.unmount();
	});

	it('dismisses only the existing draft and retries the same task slot', async () => {
		discardMock.mockResolvedValueOnce({ ok: true });
		streamMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-1', conversationId: 'conversation-1' };
			yield { type: 'task_proposal', proposal };
			yield { type: 'done' };
		});
		const dismiss = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ proposal: undefined }) }
		});
		expect(within(dismiss.container).getByRole('link', { name: /scheduled details/i })).toHaveAttribute(
			'href',
			'/scheduled?task=task-1'
		);
		await fireEvent.click(within(dismiss.container).getByRole('button', { name: 'Dismiss draft' }));
		expect(discardMock).toHaveBeenCalledWith('task-1');
		expect(within(dismiss.container).getByText(/Dismissed/i)).toBeInTheDocument();
		expect(within(dismiss.container).queryByRole('link', { name: /scheduled details/i })).not.toBeInTheDocument();
		dismiss.unmount();

		render(ScheduledArtifactCard, {
			props: { artifact: artifact({ artifactState: 'failed', retryable: true, proposal: undefined }) }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
		await waitFor(() => expect(streamMock).toHaveBeenCalledWith(
			expect.objectContaining({ taskId: 'task-1' }),
			expect.any(AbortSignal)
		));
	});

	it('hides stale task details for a persisted dismissed artifact', () => {
		const result = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ artifactState: 'dismissed', proposal: undefined }) }
		});

		expect(within(result.container).getByText(/Dismissed/i)).toBeInTheDocument();
		expect(within(result.container).queryByRole('link', { name: /scheduled details/i })).not.toBeInTheDocument();
	});

	it('keeps Adjust and Dismiss beside Schedule for a compiled draft proposal', async () => {
		streamMock.mockImplementation(async function* () {
			yield { type: 'meta', taskId: 'task-1', conversationId: 'conversation-1' };
			yield { type: 'done' };
		});
		discardMock.mockResolvedValueOnce({ ok: true });
		const result = render(ScheduledArtifactCard, { props: { artifact: artifact() } });
		expect(within(result.container).getByRole('button', { name: 'Schedule task' })).toBeInTheDocument();
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Adjust' }));
		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Adjust scheduled task' }), {
			target: { value: 'Use a shorter summary.' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Save adjustment' }));
		expect(streamMock).toHaveBeenCalledWith(
			expect.objectContaining({ taskId: 'task-1', message: 'Use a shorter summary.' }),
			expect.any(AbortSignal)
		);

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Dismiss draft' }));
		expect(discardMock).toHaveBeenCalledWith('task-1');
	});

	it('keeps proposal draft actions isolated between sibling cards', async () => {
		discardMock.mockResolvedValueOnce({ ok: true });
		const first = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ handoffId: 'handoff-a', taskId: 'task-a' }) }
		});
		const second = render(ScheduledArtifactCard, {
			props: { artifact: artifact({ handoffId: 'handoff-b', taskId: 'task-b' }) }
		});
		await fireEvent.click(within(second.container).getByRole('button', { name: 'Dismiss draft' }));
		expect(discardMock).toHaveBeenCalledWith('task-b');
		expect(within(first.container).getByRole('button', { name: 'Schedule task' })).not.toBeDisabled();
	});

	it('announces a stale 409 reload state without replacing its canonical card', async () => {
		confirmMock.mockRejectedValueOnce(new APIError(409, 'conflict'));
		getTaskMock.mockResolvedValueOnce({
			task: {
				id: 'task-1', version: 8, state: 'draft', name: 'Post-run review', kind: 'data', compiledPrompt: 'Updated.',
				timezone: 'UTC', executionMode: 'data', authorizedTools: [], deliveryPolicy: 'always', initialRun: 'wait'
			},
			definitionMessages: []
		});
		render(ScheduledArtifactCard, { props: { artifact: artifact() } });
		await fireEvent.click(screen.getByRole('button', { name: 'Schedule task' }));
		await waitFor(() => expect(screen.getByText(/changed while you were reviewing/i)).toBeInTheDocument());
		expect(screen.getAllByText('Post-run review')).toHaveLength(2);
	});

	it('keeps a canonical 409 proposal when its next adjustment fails', async () => {
		confirmMock.mockRejectedValueOnce(new APIError(409, 'conflict'));
		getTaskMock.mockResolvedValueOnce({
			task: {
				id: 'task-1', version: 8, state: 'draft', name: 'Canonical review', kind: 'data', compiledPrompt: 'Updated.',
				timezone: 'UTC', executionMode: 'data', authorizedTools: [], deliveryPolicy: 'always', initialRun: 'wait'
			},
			definitionMessages: []
		});
		streamMock.mockImplementationOnce(async function* () {
			throw new Error('definition unavailable');
		});
		const result = render(ScheduledArtifactCard, { props: { artifact: artifact() } });

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Schedule task' }));
		await waitFor(() =>
			expect(within(result.container).getAllByText('Canonical review')).toHaveLength(2)
		);
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Adjust' }));
		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Adjust scheduled task' }), {
			target: { value: 'Use a shorter summary.' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Save adjustment' }));

		await waitFor(() =>
			expect(within(result.container).getByText('definition unavailable')).toBeInTheDocument()
		);
		expect(within(result.container).getAllByText('Canonical review')).toHaveLength(2);
		expect(within(result.container).queryByText('Post-run review')).not.toBeInTheDocument();
	});

	it('keeps an empty canonical 409 draft authoritative after adjustment fails', async () => {
		confirmMock.mockRejectedValueOnce(new APIError(409, 'conflict'));
		getTaskMock.mockResolvedValueOnce({
			task: {
				id: 'task-1', version: 8, state: 'draft', name: 'Empty canonical draft', kind: 'data', compiledPrompt: '',
				timezone: 'UTC', executionMode: 'data', authorizedTools: [], deliveryPolicy: 'always', initialRun: 'wait'
			},
			definitionMessages: []
		});
		streamMock.mockImplementationOnce(async function* () {
			throw new Error('definition unavailable');
		});
		const result = render(ScheduledArtifactCard, { props: { artifact: artifact() } });

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Schedule task' }));
		await waitFor(() =>
			expect(within(result.container).getByText(/changed while you were reviewing/i)).toBeInTheDocument()
		);
		expect(within(result.container).queryByRole('button', { name: 'Schedule task' })).not.toBeInTheDocument();
		expect(within(result.container).queryByText('Post-run review')).not.toBeInTheDocument();

		await fireEvent.click(within(result.container).getByRole('button', { name: 'Adjust' }));
		await fireEvent.input(within(result.container).getByRole('textbox', { name: 'Adjust scheduled task' }), {
			target: { value: 'Rebuild the proposal.' }
		});
		await fireEvent.click(within(result.container).getByRole('button', { name: 'Save adjustment' }));

		await waitFor(() =>
			expect(within(result.container).getByText('definition unavailable')).toBeInTheDocument()
		);
		expect(within(result.container).queryByRole('button', { name: 'Schedule task' })).not.toBeInTheDocument();
		expect(within(result.container).queryByText('Post-run review')).not.toBeInTheDocument();
	});
});
