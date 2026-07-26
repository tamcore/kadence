import { fireEvent, render, screen, waitFor, within } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { capabilitiesMock, referenceOptionsMock } = vi.hoisted(() => ({
	capabilitiesMock: vi.fn(),
	referenceOptionsMock: vi.fn()
}));

vi.mock('$lib/api/documents', () => ({
	getDocumentUploadCapabilities: (...args: unknown[]) => capabilitiesMock(...args),
	listDocumentReferences: (...args: unknown[]) => referenceOptionsMock(...args)
}));

import Composer from './Composer.svelte';
import type { Document } from '$lib/types';

const privateDocument: Document = {
	id: 41,
	filename: 'my-marathon-plan.pdf',
	mime: 'application/pdf',
	source_type: 'pdf',
	scope: 'private',
	created_at: '2026-07-25T10:00:00Z'
};

const publicDocument: Document = {
	id: 72,
	filename: 'public-race-guide.md',
	mime: 'text/markdown',
	source_type: 'text',
	scope: 'public',
	created_at: '2026-07-25T11:00:00Z'
};

afterEach(() => {
	vi.clearAllMocks();
	vi.unstubAllGlobals();
});

beforeEach(() => {
	capabilitiesMock.mockResolvedValue({
		max_bytes: 10 * 1024 * 1024,
		rich_extraction: true,
		accept: 'application/pdf,.pdf,text/markdown,.md'
	});
	referenceOptionsMock.mockResolvedValue({
		own: [privateDocument],
		public: [publicDocument]
	});
});

describe('Composer', () => {
	it('keeps evidence controls disabled unless rich input is explicitly enabled', () => {
		const { container } = render(Composer, { props: { onSubmit: vi.fn() } });

		expect(container.querySelector('input[type="file"]')).not.toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Reference documents' })).not.toBeInTheDocument();
		expect(capabilitiesMock).not.toHaveBeenCalled();
	});

	it('calls onSubmit with trimmed text and clears the textarea on submit', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: '  hello world  ' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).toHaveBeenCalledWith('hello world');
		expect(textarea.value).toBe('');
	});

	it('optimistically clears and restores the exact rich input when submission is rejected', async () => {
		let resolveSubmit: (accepted: boolean) => void = () => {};
		const onSubmit = vi.fn(
			() =>
				new Promise<boolean>((resolve) => {
					resolveSubmit = resolve;
				})
		);
		const { container } = render(Composer, { props: { onSubmit, richInput: true } });
		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const screenshot = new File(['image'], 'retry.png', { type: 'image/png' });
		Object.defineProperty(input, 'files', { configurable: true, value: [screenshot] });

		await fireEvent.input(textarea, { target: { value: '  retry this exactly  ' } });
		await fireEvent.change(input);
		await fireEvent.click(screen.getByRole('button', { name: 'Reference documents' }));
		await fireEvent.click(await screen.findByRole('button', { name: 'Add public-race-guide.md' }));
		expect(
			within(screen.getByRole('list', { name: 'Documents to reference' })).getByText(
				'public-race-guide.md'
			)
		).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).toHaveBeenCalledWith(
			'retry this exactly',
			[screenshot],
			[publicDocument]
		);
		expect(textarea.value).toBe('');
		expect(screen.queryByText('retry.png')).not.toBeInTheDocument();
		expect(screen.queryByRole('list', { name: 'Documents to reference' })).not.toBeInTheDocument();

		resolveSubmit(false);
		await waitFor(() => expect(textarea.value).toBe('  retry this exactly  '));
		expect(screen.getByText('retry.png')).toBeInTheDocument();
		expect(
			within(screen.getByRole('list', { name: 'Documents to reference' })).getByText(
				'public-race-guide.md'
			)
		).toBeInTheDocument();
	});

	it('restores text when submission throws before acceptance', async () => {
		const onSubmit = vi.fn().mockRejectedValueOnce(new Error('request rejected'));
		render(Composer, { props: { onSubmit } });
		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;

		await fireEvent.input(textarea, { target: { value: 'retry after rejection' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		await waitFor(() => expect(textarea.value).toBe('retry after rejection'));
	});

	it('does not submit when disabled, even via click or Enter', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, disabled: true } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: 'hello' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));
		expect(onSubmit).not.toHaveBeenCalled();

		await fireEvent.keyDown(textarea, { key: 'Enter' });
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('submits on Enter without Shift', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: 'hello' } });
		await fireEvent.keyDown(textarea, { key: 'Enter' });

		expect(onSubmit).toHaveBeenCalledWith('hello');
	});

	it('does not submit on Shift+Enter and does not prevent default', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: 'hello' } });
		const notPrevented = await fireEvent.keyDown(textarea, { key: 'Enter', shiftKey: true });

		expect(onSubmit).not.toHaveBeenCalled();
		// fireEvent returns false if preventDefault() was called on the event
		expect(notPrevented).toBe(true);
	});

	it('does not submit empty or whitespace-only text', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit } });

		const textarea = screen.getByRole('textbox') as HTMLTextAreaElement;
		await fireEvent.input(textarea, { target: { value: '   ' } });
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('selects multiple files, removes one, and submits the remaining file without text', async () => {
		const onSubmit = vi.fn();
		const { container } = render(Composer, { props: { onSubmit, richInput: true } });
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const screenshot = new File(['image'], 'finish.png', { type: 'image/png' });
		const notes = new File(['notes'], 'week.md', { type: 'text/markdown' });
		Object.defineProperty(input, 'files', {
			configurable: true,
			value: [screenshot, notes]
		});

		await fireEvent.change(input);
		expect(screen.getByText('finish.png')).toBeInTheDocument();
		expect(screen.getByText('week.md')).toBeInTheDocument();
		await fireEvent.click(screen.getByRole('button', { name: 'Remove finish.png' }));
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).toHaveBeenCalledWith('', [notes], []);
		expect(screen.queryByText('week.md')).not.toBeInTheDocument();
	});

	it('accepts files dropped anywhere on the chat page', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, richInput: true } });
		const screenshot = new File(['image'], 'route.png', { type: 'image/png' });

		await fireEvent.dragEnter(window, {
			dataTransfer: { types: ['Files'], files: [screenshot] }
		});
		expect(screen.getByRole('status', { name: 'Chat file drop area' })).toHaveTextContent(
			'Drop files into this chat'
		);
		await fireEvent.drop(window, {
			dataTransfer: { types: ['Files'], files: [screenshot] }
		});

		expect(screen.queryByRole('status', { name: 'Chat file drop area' })).not.toBeInTheDocument();
		expect(screen.getByText('route.png')).toBeInTheDocument();
	});

	it('clears the page-wide drop overlay when dragleave omits transfer types', async () => {
		render(Composer, { props: { onSubmit: vi.fn(), richInput: true } });
		const screenshot = new File(['image'], 'route.png', { type: 'image/png' });

		await fireEvent.dragEnter(window, {
			dataTransfer: { types: ['Files'], files: [screenshot] }
		});
		expect(screen.getByRole('status', { name: 'Chat file drop area' })).toBeInTheDocument();
		await fireEvent.dragLeave(window, {
			dataTransfer: { types: [], files: [] }
		});

		expect(screen.queryByRole('status', { name: 'Chat file drop area' })).not.toBeInTheDocument();
	});

	it('adds pasted clipboard images without swallowing ordinary text paste', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, richInput: true } });
		const textarea = screen.getByRole('textbox');
		const screenshot = new File(['image'], 'clipboard.png', { type: 'image/png' });

		const imagePasteAllowed = await fireEvent.paste(textarea, {
			clipboardData: { files: [screenshot] }
		});
		expect(imagePasteAllowed).toBe(false);
		expect(screen.getByText('clipboard.png')).toBeInTheDocument();

		const textPasteAllowed = await fireEvent.paste(textarea, {
			clipboardData: { files: [] }
		});
		expect(textPasteAllowed).toBe(true);
	});

	it('enforces five files and the aggregate configured byte limit before send', async () => {
		const onSubmit = vi.fn();
		const { container } = render(Composer, { props: { onSubmit, richInput: true } });
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const files = Array.from(
			{ length: 6 },
			(_, index) => new File(['x'], `file-${index}.png`, { type: 'image/png' })
		);
		Object.defineProperty(input, 'files', { configurable: true, value: files });

		await fireEvent.change(input);
		expect(screen.getByRole('alert')).toHaveTextContent('You can attach up to 5 files.');
		expect(screen.getByText('file-4.png')).toBeInTheDocument();
		expect(screen.queryByText('file-5.png')).not.toBeInTheDocument();

		for (const file of files.slice(0, 5)) {
			await fireEvent.click(screen.getByRole('button', { name: `Remove ${file.name}` }));
		}
		const largeA = new File([new Uint8Array(6 * 1024 * 1024)], 'large-a.png', {
			type: 'image/png'
		});
		const largeB = new File([new Uint8Array(6 * 1024 * 1024)], 'large-b.png', {
			type: 'image/png'
		});
		Object.defineProperty(input, 'files', {
			configurable: true,
			value: [largeA, largeB]
		});
		await fireEvent.change(input);

		expect(screen.getByRole('alert')).toHaveTextContent(
			'Attachments exceed the 10 MB total limit.'
		);
		expect(screen.queryByText('large-a.png')).not.toBeInTheDocument();
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('searches grouped private and public references and submits a reference-only turn', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, richInput: true } });

		await fireEvent.click(screen.getByRole('button', { name: 'Reference documents' }));
		await waitFor(() => expect(screen.getByRole('heading', { name: 'Your documents' })).toBeInTheDocument());
		expect(screen.getByRole('heading', { name: 'Public docs' })).toBeInTheDocument();
		const search = screen.getByRole('searchbox', { name: 'Search documents' });
		await fireEvent.input(search, { target: { value: 'race' } });
		expect(screen.queryByText('my-marathon-plan.pdf')).not.toBeInTheDocument();
		expect(screen.getByText('public-race-guide.md')).toBeInTheDocument();

		await fireEvent.click(
			screen.getByRole('button', { name: 'Add public-race-guide.md' })
		);
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		expect(onSubmit).toHaveBeenCalledWith('', [], [publicDocument]);
	});

	it('closes the document reference picker after an accepted submission', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, richInput: true } });

		await fireEvent.click(screen.getByRole('button', { name: 'Reference documents' }));
		await fireEvent.click(
			await screen.findByRole('button', { name: 'Add public-race-guide.md' })
		);
		await fireEvent.click(screen.getByRole('button', { name: /send/i }));

		await waitFor(() =>
			expect(
				screen.queryByRole('searchbox', { name: 'Search documents' })
			).not.toBeInTheDocument()
		);
	});

	it('does not submit the enclosing chat form when Enter is pressed in reference search', async () => {
		const onSubmit = vi.fn();
		render(Composer, { props: { onSubmit, richInput: true } });
		await fireEvent.click(screen.getByRole('button', { name: 'Reference documents' }));
		const search = await screen.findByRole('searchbox', { name: 'Search documents' });
		await fireEvent.input(search, { target: { value: 'race' } });

		const notPrevented = await fireEvent.keyDown(search, { key: 'Enter' });

		expect(notPrevented).toBe(false);
		expect(onSubmit).not.toHaveBeenCalled();
	});

	it('does not mutate document references after the composer becomes disabled', async () => {
		const onSubmit = vi.fn();
		const { rerender } = render(Composer, {
			props: { onSubmit, richInput: true }
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Reference documents' }));
		const add = await screen.findByRole('button', { name: 'Add public-race-guide.md' });

		await rerender({ onSubmit, richInput: true, disabled: true });
		await fireEvent.click(add);

		expect(screen.queryByRole('list', { name: 'Documents to reference' })).not.toBeInTheDocument();
	});

	it('previews queued images and revokes every object URL after removal or unmount', async () => {
		const createObjectURL = vi.fn((file: File) => `blob:${file.name}`);
		const revokeObjectURL = vi.fn();
		vi.stubGlobal('URL', class extends URL {
			static createObjectURL = createObjectURL;
			static revokeObjectURL = revokeObjectURL;
		});
		const { container, unmount } = render(Composer, {
			props: { onSubmit: vi.fn(), richInput: true }
		});
		const input = container.querySelector('input[type="file"]') as HTMLInputElement;
		const first = new File(['first'], 'first.png', { type: 'image/png' });
		const second = new File(['second'], 'second.png', { type: 'image/png' });
		Object.defineProperty(input, 'files', { configurable: true, value: [first, second] });

		await fireEvent.change(input);
		expect(screen.getByRole('img', { name: 'Preview first.png' })).toHaveAttribute(
			'src',
			'blob:first.png'
		);
		expect(screen.getByRole('img', { name: 'Preview second.png' })).toHaveAttribute(
			'src',
			'blob:second.png'
		);

		await fireEvent.click(screen.getByRole('button', { name: 'Remove first.png' }));
		await waitFor(() => expect(revokeObjectURL).toHaveBeenCalledWith('blob:first.png'));
		unmount();
		expect(revokeObjectURL).toHaveBeenCalledWith('blob:second.png');
	});
});
