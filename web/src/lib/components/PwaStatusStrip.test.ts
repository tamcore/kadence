import { fireEvent, render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import PwaStatusStrip from './PwaStatusStrip.svelte';
// @ts-expect-error Vitest runs in Node; app tsconfig intentionally omits Node types.
import { readFileSync } from 'node:fs';

declare const process: { cwd(): string };

describe('PwaStatusStrip', () => {
	it('explains that server-backed features are unavailable while offline', () => {
		render(PwaStatusStrip, {
			online: false,
			updateAvailable: false,
			applyingUpdate: false,
			onReload: vi.fn()
		});

		expect(screen.getByRole('status')).toHaveTextContent(
			'You’re offline — server-backed features are unavailable.'
		);
	});

	it('offers an explicit reload when an update is waiting', async () => {
		const onReload = vi.fn();
		render(PwaStatusStrip, {
			online: true,
			updateAvailable: true,
			applyingUpdate: false,
			onReload
		});

		await fireEvent.click(screen.getByRole('button', { name: 'Reload' }));

		expect(onReload).toHaveBeenCalledOnce();
	});

	it('keeps offline status before the update prompt when both apply', () => {
		render(PwaStatusStrip, {
			online: false,
			updateAvailable: true,
			applyingUpdate: false,
			onReload: vi.fn()
		});

		const statuses = screen.getAllByRole('status');
		expect(statuses).toHaveLength(2);
		expect(statuses[0]).toHaveTextContent('You’re offline');
		expect(statuses[1]).toHaveTextContent('Update available');
	});

	it('disables the reload action during activation', () => {
		render(PwaStatusStrip, {
			online: true,
			updateAvailable: true,
			applyingUpdate: true,
			onReload: vi.fn()
		});

		expect(screen.getByRole('button', { name: 'Reloading…' })).toBeDisabled();
	});

	it('renders nothing while online without a waiting update', () => {
		render(PwaStatusStrip, {
			online: true,
			updateAvailable: false,
			applyingUpdate: false,
			onReload: vi.fn()
		});

		expect(screen.queryByRole('status')).not.toBeInTheDocument();
		expect(screen.queryByRole('button')).not.toBeInTheDocument();
	});
});

describe('PwaStatusStrip theming', () => {
	it('routes every colour through a token', () => {
		const source = readFileSync(
			`${process.cwd()}/src/lib/components/PwaStatusStrip.svelte`,
			'utf8'
		);
		const style = source.match(/<style[^>]*>([\s\S]*?)<\/style>/)?.[1] ?? '';
		expect(style).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
		expect(style).not.toMatch(/\brgba?\(/);
		expect(style).toContain('var(--brand)');
		expect(style).toContain('var(--on-brand)');
		expect(style).toContain('var(--warning)');
	});
});
