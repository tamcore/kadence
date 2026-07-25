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
});
