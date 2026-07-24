import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ScheduledProposal from './ScheduledProposal.svelte';

describe('ScheduledProposal', () => {
	it('summarizes the human-facing plan and confirms its exact version', async () => {
		const onConfirm = vi.fn();
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 7,
					name: 'Post-run review',
					taskKind: 'data',
					compiledPrompt: 'Analyze the latest run.',
					executionMode: 'data',
					schedule: {
						DTStart: '2026-07-25T08:00:00Z',
						RRULE: 'FREQ=DAILY',
						Timezone: 'Europe/Berlin'
					},
					timezone: 'Europe/Berlin',
					authorizedTools: ['garmin__activities'],
					deliveryPolicy: 'always',
					initialRun: 'preview'
				},
				onConfirm
			}
		});

		expect(screen.getByRole('heading', { name: 'Post-run review' })).toBeInTheDocument();
		expect(screen.getByText('Europe/Berlin')).toBeInTheDocument();
		expect(screen.getByText(/every day/i)).toBeInTheDocument();
		expect(screen.getByText(/10:00/)).toBeInTheDocument();
		expect(screen.getByText(/garmin activities/i)).toBeInTheDocument();
		expect(screen.getByText('Analyze the latest run.')).not.toBeVisible();
		await fireEvent.click(screen.getByText('View final instruction'));
		expect(screen.getByText('Analyze the latest run.')).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'Schedule task' }));
		expect(onConfirm).toHaveBeenCalledWith(7);
	});

	it('formats one-off time in the proposal timezone rather than the browser timezone', () => {
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 1,
					name: 'Check in',
					taskKind: 'reminder',
					compiledPrompt: 'Check in.',
					executionMode: 'static',
					schedule: { At: '2026-07-25T08:00:00Z', Timezone: 'America/Los_Angeles' },
					timezone: 'America/Los_Angeles',
					authorizedTools: [],
					deliveryPolicy: 'always',
					initialRun: 'wait',
					staticMessage: 'Check in.'
				},
				onConfirm: vi.fn()
			}
		});
		expect(screen.getByText(/1:00 AM/)).toBeInTheDocument();
	});

	it('preserves interval and completion constraints in the human cadence', () => {
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 2,
					name: 'Alternating brief',
					taskKind: 'data',
					compiledPrompt: 'Create a brief.',
					executionMode: 'data',
					schedule: {
						DTStart: '2026-07-25T08:00:00Z',
						RRULE: 'FREQ=DAILY;INTERVAL=2;COUNT=4',
						Timezone: 'Europe/Berlin'
					},
					timezone: 'Europe/Berlin',
					authorizedTools: [],
					deliveryPolicy: 'always',
					initialRun: 'wait'
				},
				onConfirm: vi.fn()
			}
		});
		expect(screen.getByText(/every 2 days/i)).toBeInTheDocument();
		expect(screen.getByText(/for 4 runs/i)).toBeInTheDocument();
	});

	it.each([
		'FREQ=HOURLY;BYDAY=MO',
		'FREQ=YEARLY;BYMONTHDAY=1',
		'FREQ=DAILY;BYDAY=MO',
		'FREQ=WEEKLY;BYMONTHDAY=1',
		'FREQ=MONTHLY;BYDAY=MO',
		'FREQ=MONTHLY;BYMONTHDAY=0'
	])('shows exact DTSTART and RRULE when %s cannot be fully summarized', (rrule) => {
		const dtStart = '2026-07-25T08:00:00Z';
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 3,
					name: 'Selector-sensitive cadence',
					taskKind: 'data',
					compiledPrompt: 'Run on the exact cadence.',
					executionMode: 'data',
					schedule: { DTStart: dtStart, RRULE: rrule, Timezone: 'Europe/Berlin' },
					timezone: 'Europe/Berlin',
					authorizedTools: [],
					deliveryPolicy: 'always',
					initialRun: 'wait'
				},
				onConfirm: vi.fn()
			}
		});

		expect(screen.getByText((content) => content.includes(`DTSTART: ${dtStart}`))).toBeInTheDocument();
		expect(screen.getByText((content) => content.includes(`RRULE: ${rrule}`))).toBeInTheDocument();
	});

	it.each([
		['FREQ=WEEKLY;BYDAY=MO,FR', /Every week · Mon, Fri/i],
		['FREQ=MONTHLY;BYMONTHDAY=1,-1', /Every month · on day 1, -1/i]
	])('humanizes %s when every recurrence selector is fully represented', (rrule, summary) => {
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 3,
					name: 'Fully represented cadence',
					taskKind: 'data',
					compiledPrompt: 'Run on the summarized cadence.',
					executionMode: 'data',
					schedule: {
						DTStart: '2026-07-25T08:00:00Z',
						RRULE: rrule,
						Timezone: 'Europe/Berlin'
					},
					timezone: 'Europe/Berlin',
					authorizedTools: [],
					deliveryPolicy: 'always',
					initialRun: 'wait'
				},
				onConfirm: vi.fn()
			}
		});

		expect(screen.getByText(summary)).toBeInTheDocument();
	});

	it.each(['BYHOUR=8', 'BYMINUTE=15', 'BYMONTH=7', 'BYSETPOS=1'])(
		'shows the exact RRULE when %s is not fully humanized',
		(selector) => {
			const rrule = `FREQ=MONTHLY;BYDAY=MO,TU;${selector}`;
			render(ScheduledProposal, {
				props: {
					proposal: {
						version: 3,
						name: 'Precise monthly brief',
						taskKind: 'data',
						compiledPrompt: 'Create a precise brief.',
						executionMode: 'data',
						schedule: {
							DTStart: '2026-07-25T08:00:00Z',
							RRULE: rrule,
							Timezone: 'Europe/Berlin'
						},
						timezone: 'Europe/Berlin',
						authorizedTools: [],
						deliveryPolicy: 'always',
						initialRun: 'wait'
					},
					onConfirm: vi.fn()
				}
			});

			expect(
				screen.getByText((content) => content.includes(`RRULE: ${rrule}`))
			).toBeInTheDocument();
		}
	);

	it('shows the exact RRULE when its valid frequency is not humanized', () => {
		const rrule = 'FREQ=MINUTELY;INTERVAL=60';
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 4,
					name: 'Hourly check',
					taskKind: 'data',
					compiledPrompt: 'Check once an hour.',
					executionMode: 'data',
					schedule: { DTStart: '2026-07-25T08:00:00Z', RRULE: rrule, Timezone: 'UTC' },
					timezone: 'UTC',
					authorizedTools: [],
					deliveryPolicy: 'always',
					initialRun: 'wait'
				},
				onConfirm: vi.fn()
			}
		});

		expect(screen.getByText((content) => content.includes(`RRULE: ${rrule}`))).toBeInTheDocument();
	});

	it.each(['WEEKLY', 'MONTHLY', 'YEARLY'])(
		'shows exact DTSTART and RRULE when bare %s inherits recurrence selectors',
		(frequency) => {
			const dtStart = '2026-07-25T08:00:00Z';
			const rrule = `FREQ=${frequency}`;
			render(ScheduledProposal, {
				props: {
					proposal: {
						version: 5,
						name: 'Inherited cadence',
						taskKind: 'data',
						compiledPrompt: 'Run on the inherited cadence.',
						executionMode: 'data',
						schedule: { DTStart: dtStart, RRULE: rrule, Timezone: 'Europe/Berlin' },
						timezone: 'Europe/Berlin',
						authorizedTools: [],
						deliveryPolicy: 'always',
						initialRun: 'wait'
					},
					onConfirm: vi.fn()
				}
			});

			expect(screen.getByText((content) => content.includes(`DTSTART: ${dtStart}`))).toBeInTheDocument();
			expect(screen.getByText((content) => content.includes(`RRULE: ${rrule}`))).toBeInTheDocument();
		}
	);

	it('shows exact DTSTART and RRULE when UNTIL includes a cutoff time', () => {
		const dtStart = '2026-07-25T08:00:00Z';
		const rrule = 'FREQ=DAILY;UNTIL=20260801T143000Z';
		render(ScheduledProposal, {
			props: {
				proposal: {
					version: 6,
					name: 'Timed cutoff',
					taskKind: 'data',
					compiledPrompt: 'Stop at the exact cutoff.',
					executionMode: 'data',
					schedule: { DTStart: dtStart, RRULE: rrule, Timezone: 'Europe/Berlin' },
					timezone: 'Europe/Berlin',
					authorizedTools: [],
					deliveryPolicy: 'always',
					initialRun: 'wait'
				},
				onConfirm: vi.fn()
			}
		});

		expect(screen.getByText((content) => content.includes(`DTSTART: ${dtStart}`))).toBeInTheDocument();
		expect(screen.getByText((content) => content.includes(`RRULE: ${rrule}`))).toBeInTheDocument();
	});
});
