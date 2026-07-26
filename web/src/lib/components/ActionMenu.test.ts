import { fireEvent, render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import ActionMenu from './ActionMenu.svelte';

afterEach(() => vi.restoreAllMocks());

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

	it('keeps disabled link items unavailable without navigation', async () => {
		const onShare = vi.fn();
		render(ActionMenu, {
			props: {
				label: 'Account actions',
				items: [
					{ label: 'Profile', href: '/profile' },
					{ label: 'Share', href: '/share', disabled: true, ariaLabel: 'Share (coming soon)', onSelect: onShare },
					{ separator: true }
				]
			}
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Account actions' }));
		expect(screen.getByRole('menuitem', { name: 'Profile', hidden: true })).toHaveAttribute('href', '/profile');
		const share = screen.getByRole('menuitem', { name: 'Share (coming soon)', hidden: true });
		expect(share).toHaveAttribute('aria-disabled', 'true');
		expect(share).not.toHaveAttribute('href');
		await fireEvent.click(share);
		expect(onShare).not.toHaveBeenCalled();
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

		expect(document.activeElement).toBe(rename);
		await fireEvent.keyDown(menu, { key: 'ArrowDown' });
		expect(document.activeElement).toBe(remove);
		await fireEvent.keyDown(menu, { key: 'Home' });
		expect(document.activeElement).toBe(rename);
		await fireEvent.keyDown(menu, { key: 'End' });
		expect(document.activeElement).toBe(remove);
		await fireEvent.keyDown(menu, { key: 'Escape' });
		expect(document.activeElement).toBe(trigger);
	});

	it('measures after opening and flips placement above a trigger near the viewport bottom', async () => {
		Object.defineProperty(window, 'innerWidth', { configurable: true, value: 120 });
		Object.defineProperty(window, 'innerHeight', { configurable: true, value: 768 });
		vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
			if (this.hasAttribute('data-action-menu-trigger')) {
				return { top: 700, bottom: 720, right: 118 } as DOMRect;
			}
			return { width: 104, height: 120 } as DOMRect;
		});
		render(ActionMenu, { props: { label: 'Conversation actions', items: [{ label: 'Rename' }] } });
		const trigger = screen.getByRole('button', { name: 'Conversation actions' });

		await fireEvent.click(trigger);

		expect(screen.getByRole('menu', { hidden: true })).toHaveStyle('--action-menu-left: 8px');
		expect(screen.getByRole('menu', { hidden: true })).toHaveStyle('--action-menu-top: 574px');
	});

	it('gives an oversized menu a viewport-bounded height and a scrollable menu body', async () => {
		Object.defineProperty(window, 'innerWidth', { configurable: true, value: 500 });
		Object.defineProperty(window, 'innerHeight', { configurable: true, value: 400 });
		vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
			if (this.hasAttribute('data-action-menu-trigger')) {
				return { top: 200, bottom: 220, right: 220 } as DOMRect;
			}
			return { width: 224, height: 1000 } as DOMRect;
		});
		render(ActionMenu, {
			props: { label: 'Long actions', items: Array.from({ length: 40 }, (_, index) => ({ label: `Action ${index}` })) }
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Long actions' }));

		expect(screen.getByRole('menu', { hidden: true })).toHaveStyle('max-height: 384px; overflow-y: auto');
	});
});
