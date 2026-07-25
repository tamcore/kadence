import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import DocumentUpload from './DocumentUpload.svelte';
import * as documentsApi from '$lib/api/documents';
import { APIError } from '$lib/api/client';
import type { Document } from '$lib/types';

const pdfCapabilities = {
	max_bytes: 10 * 1024 * 1024,
	rich_extraction: false,
	accept: 'application/pdf,.pdf'
};

const richCapabilities = {
	max_bytes: 20 * 1024 * 1024,
	rich_extraction: true,
	accept: 'application/pdf,.pdf,image/png,.png,text/plain,.txt'
};

function uploadedDocument(file: File, id: number): Document {
	return {
		id,
		filename: file.name,
		mime: file.type,
		source_type: file.type === 'application/pdf' ? 'pdf' : 'image',
		scope: 'private',
		created_at: '2026-07-25T10:00:00Z'
	};
}

describe('DocumentUpload', () => {
	beforeEach(() => vi.restoreAllMocks());

	it('uploads multiple selected files sequentially and reports each success', async () => {
		let active = 0;
		let maxActive = 0;
		const order: string[] = [];
		const spy = vi.spyOn(documentsApi, 'uploadDocument').mockImplementation(async (file) => {
			active += 1;
			maxActive = Math.max(maxActive, active);
			order.push(file.name);
			await Promise.resolve();
			active -= 1;
			return uploadedDocument(file, order.length);
		});
		const onUploaded = vi.fn();
		const { container } = render(DocumentUpload, { capabilities: richCapabilities, onUploaded });

		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const files = [
			new File([new Uint8Array([1])], 'plan.pdf', { type: 'application/pdf' }),
			new File([new Uint8Array([2])], 'route.png', { type: 'image/png' })
		];
		Object.defineProperty(input, 'files', { configurable: true, value: files });
		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: 'Upload 2 files' }));

		await waitFor(() => expect(spy).toHaveBeenCalledTimes(2));
		expect(order).toEqual(['plan.pdf', 'route.png']);
		expect(maxActive).toBe(1);
		expect(screen.getAllByText('Uploaded')).toHaveLength(2);
		expect(onUploaded).toHaveBeenCalledTimes(1);
	});

	it('preserves partial success and shows a friendly per-file 415 message', async () => {
		vi.spyOn(documentsApi, 'uploadDocument').mockImplementation(async (file) => {
			if (file.type === 'image/png') throw new APIError(415, 'unsupported');
			return uploadedDocument(file, 1);
		});
		const onUploaded = vi.fn();
		const { container } = render(DocumentUpload, { capabilities: richCapabilities, onUploaded });
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		Object.defineProperty(input, 'files', {
			configurable: true,
			value: [
				new File(['pdf'], 'plan.pdf', { type: 'application/pdf' }),
				new File(['png'], 'route.png', { type: 'image/png' })
			]
		});
		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: 'Upload 2 files' }));

		await waitFor(() => expect(screen.getByText("This file type isn't supported.")).toBeInTheDocument());
		expect(screen.getByText('Uploaded')).toBeInTheDocument();
		expect(screen.getByText('plan.pdf')).toBeInTheDocument();
		expect(screen.getByText('route.png')).toBeInTheDocument();
		expect(onUploaded).toHaveBeenCalledTimes(1);
	});

	it('only opens the route-wide overlay for file drags and adds dropped files to the queue', async () => {
		const spy = vi.spyOn(documentsApi, 'uploadDocument').mockResolvedValue(
			uploadedDocument(new File(['x'], 'route.png', { type: 'image/png' }), 1)
		);
		render(DocumentUpload, { capabilities: richCapabilities, onUploaded: vi.fn() });

		await fireEvent.dragEnter(window, { dataTransfer: { types: ['text/plain'], files: [] } });
		expect(screen.queryByText('Drop files to add them')).not.toBeInTheDocument();

		const file = new File(['x'], 'route.png', { type: 'image/png' });
		await fireEvent.dragEnter(window, { dataTransfer: { types: ['Files'], files: [file] } });
		expect(screen.getByRole('status', { name: 'File drop area' })).toHaveTextContent('Drop files to add them');

		await fireEvent.drop(window, { dataTransfer: { types: ['Files'], files: [file] } });
		expect(screen.queryByRole('status', { name: 'File drop area' })).not.toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Upload 1 file' })).toBeEnabled();
		expect(spy).not.toHaveBeenCalled();
	});

	it('uses the server accept profile, allows multiple files, and explains rich support', () => {
		const { container } = render(DocumentUpload, {
			capabilities: richCapabilities,
			onUploaded: vi.fn()
		});
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;

		expect(input).toHaveAttribute('multiple');
		expect(input).toHaveAttribute('accept', richCapabilities.accept);
		expect(screen.getByText(/PDF, images, text, web pages, office files, and e-books/i)).toBeInTheDocument();
	});

	it('disables file selection and the upload action while the queue is active', async () => {
		let resolveUpload!: (value: ReturnType<typeof uploadedDocument>) => void;
		vi.spyOn(documentsApi, 'uploadDocument').mockImplementation(
			(file) =>
				new Promise((resolve) => {
					resolveUpload = resolve;
				})
		);
		const { container } = render(DocumentUpload, {
			capabilities: pdfCapabilities,
			onUploaded: vi.fn()
		});
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const file = new File(['pdf'], 'plan.pdf', { type: 'application/pdf' });
		Object.defineProperty(input, 'files', { configurable: true, value: [file] });
		await fireEvent.change(input);
		const button = screen.getByRole('button', { name: 'Upload 1 file' });

		await fireEvent.click(button);
		await waitFor(() => expect(screen.getByText('Uploading…')).toBeInTheDocument());
		expect(input).toBeDisabled();
		expect(button).toBeDisabled();

		resolveUpload(uploadedDocument(file, 1));
		await waitFor(() => expect(screen.getByText('Uploaded')).toBeInTheDocument());
	});

	it('shows the configured size limit for a 413 response', async () => {
		vi.spyOn(documentsApi, 'uploadDocument').mockRejectedValue(new APIError(413, 'too large'));
		const { container } = render(DocumentUpload, {
			capabilities: pdfCapabilities,
			onUploaded: vi.fn()
		});
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		Object.defineProperty(input, 'files', {
			configurable: true,
			value: [new File(['pdf'], 'plan.pdf', { type: 'application/pdf' })]
		});
		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: 'Upload 1 file' }));

		await waitFor(() => expect(screen.getByText('File exceeds the 10 MB upload limit.')).toBeInTheDocument());
	});
});
