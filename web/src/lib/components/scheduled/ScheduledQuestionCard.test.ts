import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ScheduledQuestionCard from './ScheduledQuestionCard.svelte';

describe('ScheduledQuestionCard', () => {
	it('answers a single choice immediately', async () => {
		const onAnswer = vi.fn();
		render(ScheduledQuestionCard, {
			props: {
				question: {
					id: 'cadence',
					prompt: 'How often?',
					kind: 'single_select',
					options: [
						{ label: 'Every day', value: 'daily' },
						{ label: 'Every week', value: 'weekly' }
					],
					allowCustom: false,
					optional: false
				},
				onAnswer
			}
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Every day' }));
		expect(onAnswer).toHaveBeenCalledWith('daily');
	});

	it('offers Skip for an optional single choice and lets the user back out or close', async () => {
		const onAnswer = vi.fn();
		const onBack = vi.fn();
		const onClose = vi.fn();
		render(ScheduledQuestionCard, {
			props: {
				question: {
					id: 'focus',
					prompt: 'Choose a focus',
					kind: 'single_select',
					options: [{ label: 'Pace', value: 'pace' }],
					allowCustom: false,
					optional: true
				},
				onAnswer,
				onBack,
				onClose
			}
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Back' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Close question' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Skip' }));
		expect(onBack).toHaveBeenCalled();
		expect(onClose).toHaveBeenCalled();
		expect(onAnswer).toHaveBeenCalledWith('Skip');
	});

	it('submits multiple values only on Continue', async () => {
		const onAnswer = vi.fn();
		render(ScheduledQuestionCard, {
			props: {
				question: {
					id: 'metrics',
					prompt: 'What should I compare?',
					kind: 'multi_select',
					options: [
						{ label: 'Pace', value: 'pace' },
						{ label: 'Heart rate', value: 'heart_rate' }
					],
					allowCustom: true,
					optional: true
				},
				onAnswer
			}
		});
		await fireEvent.click(screen.getByRole('checkbox', { name: 'Pace' }));
		await fireEvent.input(screen.getByLabelText('Something else'), {
			target: { value: 'Elevation' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		expect(onAnswer).toHaveBeenCalledWith('pace, Elevation');
	});

	it('supports free text and optional Skip', async () => {
		const onAnswer = vi.fn();
		render(ScheduledQuestionCard, {
			props: {
				question: {
					id: 'focus',
					prompt: 'Any focus?',
					kind: 'text',
					allowCustom: true,
					optional: true
				},
				onAnswer
			}
		});
		expect(screen.getByRole('button', { name: 'Skip' })).toBeInTheDocument();
		await fireEvent.input(screen.getByLabelText('Your answer'), {
			target: { value: '  Recovery trend  ' }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Continue' }));
		expect(onAnswer).toHaveBeenCalledWith('Recovery trend');
	});

	it('restores a saved single-choice answer', () => {
		render(ScheduledQuestionCard, {
			props: {
				question: {
					id: 'cadence',
					prompt: 'How often?',
					kind: 'single_select',
					options: [
						{ label: 'Every day', value: 'daily' },
						{ label: 'Every week', value: 'weekly' }
					],
					allowCustom: false,
					optional: false
				},
				initialAnswer: 'weekly',
				onAnswer: vi.fn()
			}
		});

		expect(screen.getByRole('button', { name: 'Every week' })).toHaveAttribute(
			'aria-pressed',
			'true'
		);
	});

	it('restores saved multi-choice and custom answers', () => {
		render(ScheduledQuestionCard, {
			props: {
				question: {
					id: 'focus',
					prompt: 'What matters?',
					kind: 'multi_select',
					options: [
						{ label: 'Pace', value: 'pace' },
						{ label: 'Heart rate', value: 'heart_rate' }
					],
					allowCustom: true,
					optional: false
				},
				initialAnswer: 'pace, Elevation',
				onAnswer: vi.fn()
			}
		});

		expect(screen.getByRole('checkbox', { name: 'Pace' })).toBeChecked();
		expect(screen.getByLabelText('Something else')).toHaveValue('Elevation');
	});
});
