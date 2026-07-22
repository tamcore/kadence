import { render, screen, fireEvent } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it, vi } from 'vitest';
import Modal from './Modal.svelte';

function bodySnippet(text: string) {
	return createRawSnippet(() => ({
		render: () => `<p>${text}</p>`
	}));
}

describe('Modal', () => {
	it('renders nothing when closed', () => {
		render(Modal, {
			open: false,
			title: 'Rename conversation',
			onClose: vi.fn(),
			children: bodySnippet('body content')
		});
		expect(screen.queryByText('Rename conversation')).not.toBeInTheDocument();
		expect(screen.queryByText('body content')).not.toBeInTheDocument();
	});

	it('renders the title and children when open', () => {
		render(Modal, {
			open: true,
			title: 'Rename conversation',
			onClose: vi.fn(),
			children: bodySnippet('body content')
		});
		expect(screen.getByText('Rename conversation')).toBeInTheDocument();
		expect(screen.getByText('body content')).toBeInTheDocument();
		expect(screen.getByRole('dialog')).toBeInTheDocument();
	});

	it('calls onClose when the close button is clicked', async () => {
		const onClose = vi.fn();
		render(Modal, { open: true, title: 'Title', onClose, children: bodySnippet('x') });
		await fireEvent.click(screen.getByRole('button', { name: 'Close' }));
		expect(onClose).toHaveBeenCalled();
	});

	it('calls onClose when the backdrop is clicked but not when the card is clicked', async () => {
		const onClose = vi.fn();
		render(Modal, { open: true, title: 'Title', onClose, children: bodySnippet('x') });
		await fireEvent.click(screen.getByRole('dialog'));
		expect(onClose).not.toHaveBeenCalled();
		expect(screen.getByText('Title').closest('.backdrop')).not.toBeNull();
	});

	it('calls onClose on Escape keydown', async () => {
		const onClose = vi.fn();
		render(Modal, { open: true, title: 'Title', onClose, children: bodySnippet('x') });
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onClose).toHaveBeenCalled();
	});

	it('does not react to Escape when closed', async () => {
		const onClose = vi.fn();
		render(Modal, { open: false, title: 'Title', onClose, children: bodySnippet('x') });
		await fireEvent.keyDown(window, { key: 'Escape' });
		expect(onClose).not.toHaveBeenCalled();
	});
});
