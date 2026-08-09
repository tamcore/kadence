import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ActionMenu from './ActionMenu.svelte';
import ConfirmDialog from './ConfirmDialog.svelte';

async function openDialogFromActionMenu(): Promise<HTMLElement> {
	let unmountDialog = () => {};
	render(ActionMenu, {
		props: {
			label: 'Thing actions',
			items: [
				{
					label: 'Delete',
					onSelect: () => {
						const dialog = render(ConfirmDialog, {
							open: true,
							title: 'Delete thing',
							message: 'Are you sure?',
							onConfirm: vi.fn(),
							onCancel: () => unmountDialog()
						});
						unmountDialog = dialog.unmount;
					}
				}
			]
		}
	});

	const trigger = screen.getByRole('button', { name: 'Thing actions' });
	await fireEvent.click(trigger);
	await fireEvent.click(screen.getByRole('menuitem', { name: 'Delete', hidden: true }));
	const confirm = await screen.findByRole('button', { name: 'Delete' });
	await waitFor(() => expect(confirm).toHaveFocus());
	return trigger;
}

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

	it('submits confirmation once through the dialog form', async () => {
		const onConfirm = vi.fn();
		render(ConfirmDialog, {
			open: true,
			title: 'Delete thing',
			message: 'Are you sure?',
			onConfirm,
			onCancel: vi.fn()
		});
		const confirm = screen.getByRole('button', { name: 'Delete' });
		const form = confirm.closest('form');

		expect(confirm).toHaveAttribute('type', 'submit');
		expect(confirm).toHaveAttribute('autofocus');
		expect(form).not.toBeNull();
		await fireEvent.submit(form!);
		expect(onConfirm).toHaveBeenCalledOnce();
	});

	it.each(['Cancel', 'Escape'])('restores the action menu trigger after closing with %s', async (closeMethod) => {
		const trigger = await openDialogFromActionMenu();

		if (closeMethod === 'Cancel') await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
		else await fireEvent.keyDown(window, { key: 'Escape' });

		expect(trigger.isConnected).toBe(true);
		await waitFor(() => expect(trigger).toHaveFocus());
	});

	it('keeps focus outside the dialog when it was moved before close', async () => {
		await openDialogFromActionMenu();
		render(ActionMenu, { props: { label: 'Other actions', items: [{ label: 'Rename' }] } });

		const outside = screen.getByRole('button', { name: 'Other actions' });
		outside.focus();

		await fireEvent.keyDown(window, { key: 'Escape' });

		expect(outside).toHaveFocus();
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
		const cancel = screen.getByRole('button', { name: 'Cancel' });
		expect(cancel).toHaveAttribute('type', 'button');
		await fireEvent.click(cancel);
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
