import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import ActionMenu from './ActionMenu.svelte';

describe('ActionMenu', () => {
	it('opens from its trigger, runs a menu action, and restores focus', async () => {
		const onRename = vi.fn();
		render(ActionMenu, {
			props: {
				label: 'Conversation actions',
				items: [{ label: 'Rename', onSelect: onRename }]
			}
		});

		const trigger = screen.getByRole('button', { name: 'Conversation actions' });
		await fireEvent.click(trigger);
		expect(screen.getByRole('menu', { hidden: true })).toBeInTheDocument();

		await fireEvent.click(screen.getByRole('menuitem', { name: 'Rename', hidden: true }));
		expect(onRename).toHaveBeenCalledOnce();
		expect(screen.queryByRole('menu', { hidden: true })).not.toBeInTheDocument();
		expect(document.activeElement).toBe(trigger);
	});

	it('keeps disabled items unavailable and supports link items', async () => {
		render(ActionMenu, {
			props: {
				label: 'Account actions',
				items: [
					{ label: 'Profile', href: '/profile' },
					{ label: 'Archive', disabled: true },
					{ separator: true }
				]
			}
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Account actions' }));
		expect(screen.getByRole('menuitem', { name: 'Profile', hidden: true })).toHaveAttribute('href', '/profile');
		expect(screen.getByRole('menuitem', { name: 'Archive', hidden: true })).toBeDisabled();
		expect(screen.getByRole('separator', { hidden: true })).toBeInTheDocument();
	});

	it('closes on an outside click and restores focus to its trigger', async () => {
		render(ActionMenu, {
			props: { label: 'Conversation actions', items: [{ label: 'Rename' }] }
		});
		const trigger = screen.getByRole('button', { name: 'Conversation actions' });
		await fireEvent.click(trigger);
		await fireEvent.pointerDown(document.body);

		expect(screen.queryByRole('menu', { hidden: true })).not.toBeInTheDocument();
		expect(document.activeElement).toBe(trigger);
	});

	it('keeps only one menu open at a time', async () => {
		render(ActionMenu, { props: { label: 'First actions', items: [{ label: 'Rename' }] } });
		render(ActionMenu, { props: { label: 'Second actions', items: [{ label: 'Rename' }] } });

		await fireEvent.click(screen.getByRole('button', { name: 'First actions' }));
		await fireEvent.click(screen.getByRole('button', { name: 'Second actions' }));

		expect(screen.getAllByRole('menu', { hidden: true })).toHaveLength(1);
	});

	it('supports arrow-key navigation and Escape focus restoration', async () => {
		render(ActionMenu, {
			props: { label: 'Conversation actions', items: [{ label: 'Rename' }, { label: 'Delete' }] }
		});
		const trigger = screen.getByRole('button', { name: 'Conversation actions' });
		await fireEvent.click(trigger);
		const menu = screen.getByRole('menu', { hidden: true });
		const [rename, remove] = screen.getAllByRole('menuitem', { hidden: true });

		await fireEvent.keyDown(menu, { key: 'ArrowDown' });
		expect(document.activeElement).toBe(rename);
		await fireEvent.keyDown(menu, { key: 'ArrowDown' });
		expect(document.activeElement).toBe(remove);
		await fireEvent.keyDown(menu, { key: 'Escape' });
		expect(document.activeElement).toBe(trigger);
	});

	it('clamps its top-layer placement to the viewport edge', async () => {
		Object.defineProperty(window, 'innerWidth', { configurable: true, value: 120 });
		render(ActionMenu, { props: { label: 'Conversation actions', items: [{ label: 'Rename' }] } });
		const trigger = screen.getByRole('button', { name: 'Conversation actions' });
		vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({
			bottom: 20,
			right: 118
		} as DOMRect);

		await fireEvent.click(trigger);

		expect(screen.getByRole('menu', { hidden: true })).toHaveStyle('--action-menu-left: 8px');
	});
});
