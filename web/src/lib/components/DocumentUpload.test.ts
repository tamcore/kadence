import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import DocumentUpload from './DocumentUpload.svelte';
import * as documentsApi from '$lib/api/documents';
import { APIError } from '$lib/api/client';

describe('DocumentUpload', () => {
	beforeEach(() => vi.restoreAllMocks());

	it('uploads the selected file and calls onUploaded', async () => {
		const spy = vi.spyOn(documentsApi, 'uploadDocument').mockResolvedValue({
			id: 1, filename: 'p.pdf', mime: 'application/pdf', source_type: 'pdf', scope: 'private', created_at: 'x'
		});
		const onUploaded = vi.fn();
		const { container } = render(DocumentUpload, { onUploaded });

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File([new Uint8Array([1])], 'p.pdf', { type: 'application/pdf' });
		Object.defineProperty(input, 'files', { value: [file] });
		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: /upload/i }));

		await waitFor(() => expect(spy).toHaveBeenCalled());
		expect(onUploaded).toHaveBeenCalled();
	});

	it('shows a friendly message on 415', async () => {
		vi.spyOn(documentsApi, 'uploadDocument').mockRejectedValue(new APIError(415, 'unsupported'));
		const { container } = render(DocumentUpload, { onUploaded: vi.fn() });
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		Object.defineProperty(input, 'files', { value: [new File(['x'], 'x.png', { type: 'image/png' })] });
		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: /upload/i }));
		await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/only pdf/i));
	});
});
