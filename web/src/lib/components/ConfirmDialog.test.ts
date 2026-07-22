import { render, screen, fireEvent } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ConfirmDialog from './ConfirmDialog.svelte';

describe('ConfirmDialog', () => {
	it('renders nothing when closed', () => {
		render(ConfirmDialog, {
			open: false,
			title: 'Delete thing',
			message: 'Are you sure?',
			onConfirm: vi.fn(),
			onCancel: vi.fn()
		});
		expect(screen.queryByText('Are you sure?')).not.toBeInTheDocument();
	});

	it('calls onConfirm when the confirm button is clicked', async () => {
		const onConfirm = vi.fn();
		render(ConfirmDialog, {
			open: true,
			title: 'Delete thing',
			message: 'Are you sure?',
			onConfirm,
			onCancel: vi.fn()
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
		expect(onConfirm).toHaveBeenCalled();
	});

	it('calls onCancel and not onConfirm when cancel is clicked', async () => {
		const onConfirm = vi.fn();
		const onCancel = vi.fn();
		render(ConfirmDialog, {
			open: true,
			title: 'Delete thing',
			message: 'Are you sure?',
			onConfirm,
			onCancel
		});
		await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		expect(onCancel).toHaveBeenCalled();
		expect(onConfirm).not.toHaveBeenCalled();
	});

	it('supports a custom confirm label', () => {
		render(ConfirmDialog, {
			open: true,
			title: 'Sign out other devices',
			message: 'This will end all other sessions.',
			confirmLabel: 'Sign out others',
			onConfirm: vi.fn(),
			onCancel: vi.fn()
		});
		expect(screen.getByRole('button', { name: 'Sign out others' })).toBeInTheDocument();
	});
});
