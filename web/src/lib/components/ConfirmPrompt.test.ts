import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { get } from 'svelte/store';
import ConfirmPrompt from './ConfirmPrompt.svelte';
import * as confirmationsApi from '$lib/api/confirmations';
import { confirmRequest } from '$lib/stores/chat';
import type { ConfirmRequest } from '$lib/types';

const sampleRequest: ConfirmRequest = {
	requestId: 'req-1',
	tool: 'garmin__delete_workout',
	message: 'Delete workout 12? This cannot be undone.'
};

describe('ConfirmPrompt', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		confirmRequest.set(sampleRequest);
	});

	it('shows the question and names the tool being confirmed', () => {
		render(ConfirmPrompt, { request: sampleRequest });

		expect(screen.getByText(sampleRequest.message)).toBeInTheDocument();
		expect(screen.getByText(sampleRequest.tool)).toBeInTheDocument();
	});

	it('sends an allow and clears the prompt', async () => {
		const spy = vi.spyOn(confirmationsApi, 'submitConfirmation').mockResolvedValue(undefined);
		render(ConfirmPrompt, { request: sampleRequest });

		await fireEvent.click(screen.getByRole('button', { name: 'Allow' }));

		await waitFor(() => expect(spy).toHaveBeenCalledWith('req-1', true));
		await waitFor(() => expect(get(confirmRequest)).toBeNull());
	});

	it('sends a decline and clears the prompt', async () => {
		const spy = vi.spyOn(confirmationsApi, 'submitConfirmation').mockResolvedValue(undefined);
		render(ConfirmPrompt, { request: sampleRequest });

		await fireEvent.click(screen.getByRole('button', { name: 'Decline' }));

		await waitFor(() => expect(spy).toHaveBeenCalledWith('req-1', false));
		await waitFor(() => expect(get(confirmRequest)).toBeNull());
	});

	it('reports an answer that could not be delivered', async () => {
		vi.spyOn(confirmationsApi, 'submitConfirmation').mockRejectedValue(new Error('offline'));
		render(ConfirmPrompt, { request: sampleRequest });

		await fireEvent.click(screen.getByRole('button', { name: 'Allow' }));

		await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
	});

	it('does not send twice while an answer is in flight', async () => {
		const spy = vi.spyOn(confirmationsApi, 'submitConfirmation').mockImplementation(
			() => new Promise(() => {}) // never settles
		);
		render(ConfirmPrompt, { request: sampleRequest });

		const allow = screen.getByRole('button', { name: 'Allow' });
		await fireEvent.click(allow);
		await fireEvent.click(allow);

		expect(spy).toHaveBeenCalledTimes(1);
	});
});
