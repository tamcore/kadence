import { describe, expect, it, vi } from 'vitest';

const streamMock = vi.fn();
const confirmMock = vi.fn();
const detailMock = vi.fn();

vi.mock('$lib/api/scheduled', async (importOriginal) => {
	const original = await importOriginal<typeof import('$lib/api/scheduled')>();
	return {
		...original,
		streamScheduledDefinition: (...args: unknown[]) => streamMock(...args),
		confirmScheduledTask: (...args: unknown[]) => confirmMock(...args),
		getScheduledTask: (...args: unknown[]) => detailMock(...args)
	};
});

import { APIError } from '$lib/api/client';
import { ScheduledDefinitionController } from './definition.svelte';

const question = {
	id: 'cadence',
	prompt: 'How often?',
	kind: 'single_select' as const,
	options: [{ label: 'Daily', value: 'daily' }],
	allowCustom: false,
	optional: false
};

const proposal = {
	version: 3,
	name: 'Daily review',
	taskKind: 'data' as const,
	compiledPrompt: 'Review the latest run.',
	executionMode: 'data' as const,
	schedule: { RRULE: 'FREQ=DAILY', Timezone: 'Europe/Berlin' },
	timezone: 'Europe/Berlin',
	authorizedTools: [],
	deliveryPolicy: 'always' as const,
	initialRun: 'wait' as const
};

async function* events(items: unknown[]) {
	for (const item of items) yield item;
}

describe('ScheduledDefinitionController', () => {
	it('records question history and replaces the active proposal during a refinement', async () => {
		streamMock
			.mockImplementationOnce(() =>
				events([
					{ type: 'meta', taskId: 'task-1', conversationId: 'conv-1' },
					{ type: 'text', delta: 'Choose a cadence.' },
					{ type: 'task_question', question },
					{ type: 'done' }
				])
			)
			.mockImplementationOnce(() =>
				events([
					{ type: 'text', delta: 'Ready.' },
					{ type: 'task_proposal', proposal },
					{ type: 'done' }
				])
			);
		const controller = new ScheduledDefinitionController(null);

		await controller.refine('Review my run');
		expect(controller.taskId).toBe('task-1');
		expect(controller.turns).toEqual([
			{ role: 'user', content: 'Review my run' },
			{ role: 'assistant', content: 'Choose a cadence.' }
		]);
		expect(controller.questionHistory).toEqual([{ question }]);

		controller.answerQuestion('daily');
		await vi.waitFor(() => expect(controller.proposal).toEqual(proposal));
		expect(controller.question).toBeNull();
		expect(controller.questionHistory).toEqual([{ question, answer: 'daily' }]);
	});

	it('confirms the rendered proposal version', async () => {
		const controller = new ScheduledDefinitionController('task-1');
		confirmMock.mockResolvedValueOnce({ id: 'task-1', state: 'active' });

		await expect(controller.confirm(7)).resolves.toEqual({ id: 'task-1', state: 'active' });
		expect(confirmMock).toHaveBeenCalledWith('task-1', 7);
	});

	it('reloads canonical draft state after a version conflict', async () => {
		const controller = new ScheduledDefinitionController('task-1');
		confirmMock.mockRejectedValueOnce(new APIError(409, 'version changed'));
		detailMock.mockResolvedValueOnce({
			task: {
				id: 'task-1',
				state: 'draft',
				version: proposal.version,
				name: proposal.name,
				kind: proposal.taskKind,
				compiledPrompt: proposal.compiledPrompt,
				executionMode: proposal.executionMode,
				oneOffAt: undefined,
				dtStart: undefined,
				rrule: 'FREQ=DAILY',
				timezone: proposal.timezone,
				authorizedTools: proposal.authorizedTools,
				deliveryPolicy: proposal.deliveryPolicy,
				initialRun: proposal.initialRun
			},
			runs: [],
			definitionMessages: [{ role: 'assistant', text: 'The latest plan is ready.' }]
		});

		await expect(controller.confirm(2)).resolves.toBeNull();
		expect(detailMock).toHaveBeenCalledWith('task-1');
		expect(controller.stale).toBe(true);
		expect(controller.proposal).toMatchObject({ version: proposal.version, name: proposal.name });
	});

	it('keeps pending and error state isolated per card controller', async () => {
		let release!: () => void;
		streamMock.mockImplementationOnce(
			() =>
				(async function* () {
					await new Promise<void>((resolve) => (release = resolve));
					yield { type: 'error', error: 'first failed' };
				})()
		);
		const first = new ScheduledDefinitionController('first');
		const second = new ScheduledDefinitionController('second');
		const pending = first.refine('first card');

		expect(first.sending).toBe(true);
		expect(second.sending).toBe(false);
		expect(second.error).toBe('');
		release();
		await pending;
		expect(first.error).toBe('first failed');
		expect(second.error).toBe('');
	});

	it('resets all card-local definition state', () => {
		const controller = new ScheduledDefinitionController('task-1');
		controller.question = question;
		controller.proposal = proposal;
		controller.coachText = 'Thinking';
		controller.error = 'Oops';
		controller.stale = true;
		controller.reset();

		expect(controller.taskId).toBeNull();
		expect(controller.question).toBeNull();
		expect(controller.proposal).toBeNull();
		expect(controller.coachText).toBe('');
		expect(controller.error).toBe('');
		expect(controller.stale).toBe(false);
	});
});
