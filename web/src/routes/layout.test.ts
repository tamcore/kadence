import { render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { beginUploadBatch, dismissUploadBatch, uploadBatch } from '$lib/stores/upload-progress';
import { get } from 'svelte/store';

type TestPage = { url: URL; params: Record<string, string> };

const getCurrentUserMock = vi.fn();
const gotoMock = vi.fn();
const afterNavigateMock = vi.fn();
const { pageStore } = vi.hoisted(() => ({
	pageStore: (() => {
		let value: TestPage = { url: new URL('https://kadence.example/login'), params: {} };
		const subscribers = new Set<(value: TestPage) => void>();
		return {
			subscribe(run: (value: TestPage) => void) {
				run(value);
				subscribers.add(run);
				return () => subscribers.delete(run);
			},
			set(next: TestPage) {
				value = next;
				for (const run of subscribers) run(value);
			}
		};
	})()
}));

vi.mock('$app/stores', () => ({
	page: pageStore
}));

vi.mock('$app/navigation', () => ({
	goto: (...args: unknown[]) => gotoMock(...args),
	afterNavigate: (callback: () => void) => afterNavigateMock(callback)
}));

vi.mock('$lib/api/client', () => {
	class APIError extends Error {
		constructor(
			public status: number,
			message: string
		) {
			super(message);
		}
	}
	return {
		APIError,
		api: {
			getCurrentUser: () => getCurrentUserMock()
		}
	};
});

vi.mock('$lib/api/context', () => ({ getOverview: vi.fn().mockResolvedValue({ reindex: { stale: 0, total: 0 } }) }));
vi.mock('$lib/api/mcp', () => ({ listMcp: vi.fn().mockResolvedValue({ servers: [] }) }));

import Layout from './+layout.svelte';

afterEach(() => {
	const batch = get(uploadBatch);
	if (batch) dismissUploadBatch(batch.id);
	vi.clearAllMocks();
	Object.defineProperty(navigator, 'onLine', { configurable: true, value: true });
	pageStore.set({ url: new URL('https://kadence.example/login'), params: {} });
});

describe('root layout PWA state', () => {
	it('shows the offline strip when session bootstrap cannot reach the server', async () => {
		Object.defineProperty(navigator, 'onLine', { configurable: true, value: false });
		getCurrentUserMock.mockRejectedValueOnce(new TypeError('Failed to fetch'));
		const children = createRawSnippet(() => ({ render: () => '<p>Login screen</p>' }));

		render(Layout, { children });

		await waitFor(() => expect(screen.getByText('Login screen')).toBeInTheDocument());
		expect(screen.getByRole('status')).toHaveTextContent('You’re offline');
		expect(gotoMock).not.toHaveBeenCalled();
	});

	it('keeps authenticated content inside the viewport scroll owner', async () => {
		pageStore.set({ url: new URL('https://kadence.example/documents'), params: {} });
		getCurrentUserMock.mockResolvedValueOnce({ id: 1, username: 'coach', scheduledEnabled: false });
		const children = createRawSnippet(() => ({ render: () => '<p>Documents</p>' }));

		const { container } = render(Layout, { children });

		await waitFor(() => expect(container.querySelector('.app-viewport')).not.toBeNull());
		const viewport = container.querySelector('.app-viewport');
		expect(viewport).not.toBeNull();
		expect(viewport!.querySelector('.main > main')).not.toBeNull();
	});

	it('links the mobile Kadence brand to the home page', async () => {
		pageStore.set({ url: new URL('https://kadence.example/documents'), params: {} });
		getCurrentUserMock.mockResolvedValueOnce({ id: 1, username: 'coach', scheduledEnabled: false });
		const children = createRawSnippet(() => ({ render: () => '<p>Documents</p>' }));

		const { container } = render(Layout, { children });

		await waitFor(() => expect(container.querySelector('.mobilebar .brand-sm')).not.toBeNull());
		const brand = container.querySelector('.mobilebar .brand-sm');
		expect(brand?.tagName).toBe('A');
		expect(brand).toHaveAttribute('href', '/');
	});

	it('makes the app viewport inert while a global upload batch is active', async () => {
		pageStore.set({ url: new URL('https://kadence.example/documents'), params: {} });
		getCurrentUserMock.mockResolvedValueOnce({ id: 1, username: 'coach', scheduledEnabled: false });
		const children = createRawSnippet(() => ({ render: () => '<p>Documents</p>' }));
		const { container } = render(Layout, { children });

		await waitFor(() => expect(container.querySelector('.app-viewport')).not.toBeNull());
		const id = beginUploadBatch([new File(['content'], 'plan.pdf')]);

		await waitFor(() => expect((container.querySelector('.app-viewport') as HTMLElement).inert).toBe(true));
		dismissUploadBatch(id);
		await waitFor(() => expect((container.querySelector('.app-viewport') as HTMLElement).inert).not.toBe(true));
	});
});
