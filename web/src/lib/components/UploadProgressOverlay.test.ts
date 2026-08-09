import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { tick } from 'svelte';
import { afterEach, describe, expect, it } from 'vitest';
import UploadProgressOverlay from './UploadProgressOverlay.svelte';
import {
	beginUploadBatch,
	dismissUploadBatch,
	setUploadFileState,
	uploadBatch
} from '$lib/stores/upload-progress';
import { get } from 'svelte/store';

function files(...names: string[]): File[] {
	return names.map((name) => new File(['content'], name));
}

afterEach(() => {
	const batch = get(uploadBatch);
	if (batch) dismissUploadBatch(batch.id);
	cleanup();
});

describe('UploadProgressOverlay', () => {
	it('announces ordered upload lifecycle rows in a modal dialog', async () => {
		render(UploadProgressOverlay);
		const id = beginUploadBatch(files('first.pdf', 'second.png'));
		setUploadFileState(id, 0, 'uploading');
		setUploadFileState(id, 1, 'processing');
		await tick();

		const dialog = screen.getByRole('dialog', { name: 'Uploading files' });
		expect(dialog).toHaveAttribute('aria-modal', 'true');
		expect(screen.getByRole('status')).toHaveTextContent(/first\.pdf\s+Uploading…\s+second\.png\s+Processing…/);
		expect(screen.getByRole('status')).toHaveAttribute('aria-live', 'polite');
		expect(screen.queryByRole('button', { name: 'Dismiss' })).not.toBeInTheDocument();
	});

	it('keeps active uploads open when Escape or the backdrop is used', async () => {
		render(UploadProgressOverlay);
		beginUploadBatch(files('plan.pdf'));
		await tick();

		await fireEvent.keyDown(window, { key: 'Escape' });
		await fireEvent.click(screen.getByTestId('upload-progress-backdrop'));

		expect(screen.getByRole('dialog', { name: 'Uploading files' })).toBeInTheDocument();
	});

	it('focuses its heading and restores prior focus after an errored batch is dismissed', async () => {
		const { container } = render(UploadProgressOverlay);
		const trigger = document.createElement('button');
		trigger.textContent = 'Start upload';
		container.appendChild(trigger);
		trigger.focus();
		const id = beginUploadBatch(files('plan.pdf'));
		setUploadFileState(id, 0, 'error', 'Network unavailable');

		const heading = await screen.findByRole('heading', { name: 'Uploading files' });
		await waitFor(() => expect(document.activeElement).toBe(heading));
		expect(screen.getByText('Network unavailable')).toBeInTheDocument();

		await fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
		expect(screen.queryByRole('dialog', { name: 'Uploading files' })).not.toBeInTheDocument();
		expect(document.activeElement).toBe(trigger);
	});
});
