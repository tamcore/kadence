import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import type { Writable } from 'svelte/store';
import * as documentsApi from '$lib/api/documents';

const gotoMock = vi.fn();
vi.mock('$app/navigation', () => ({ goto: (...a: unknown[]) => gotoMock(...a) }));

// A mutable store so each test can control the admin flag without resetting
// the module registry (resetModules would load a second Svelte runtime
// instance alongside the one @testing-library/svelte already wired up,
// triggering `effect_orphan`). Built inside vi.hoisted (rather than importing
// `writable` from 'svelte/store' at module scope) so it exists before the
// vi.mock factory below — which vitest hoists above regular imports — runs.
const { isAdminStore } = vi.hoisted(() => {
	let value = false;
	const subscribers = new Set<(v: boolean) => void>();
	const store: Writable<boolean> = {
		subscribe(run: (v: boolean) => void) {
			run(value);
			subscribers.add(run);
			return () => subscribers.delete(run);
		},
		set(v: boolean) {
			value = v;
			subscribers.forEach((run) => run(value));
		},
		update(fn: (v: boolean) => boolean) {
			store.set(fn(value));
		}
	};
	return { isAdminStore: store };
});
vi.mock('$lib/stores/auth', () => ({ isAdmin: isAdminStore }));

import Page from './+page.svelte';

describe('/admin/documents', () => {
	beforeEach(() => {
		vi.restoreAllMocks();
		gotoMock.mockClear();
		isAdminStore.set(false);
		vi.spyOn(documentsApi, 'getDocumentUploadCapabilities').mockResolvedValue({
			max_bytes: 10 * 1024 * 1024,
			rich_extraction: false,
			accept: 'application/pdf,.pdf'
		});
	});

	it('redirects a non-admin to /', async () => {
		isAdminStore.set(false);

		render(Page);

		await waitFor(() => expect(gotoMock).toHaveBeenCalledWith('/'));
	});

	it('loads public documents for an admin', async () => {
		isAdminStore.set(true);

		const spy = vi.spyOn(documentsApi, 'listDocuments').mockResolvedValue([
			{
				id: 2,
				filename: 'shared.pdf',
				mime: 'application/pdf',
				source_type: 'pdf',
				scope: 'public',
				created_at: '2026-07-19T10:00:00Z'
			}
		]);

		render(Page);

		await waitFor(() => expect(screen.getByText('shared.pdf')).toBeInTheDocument());
		expect(spy).toHaveBeenCalledWith({ admin: true });
	});

	it('uses the same rich upload capability profile as the private page', async () => {
		isAdminStore.set(true);
		vi.mocked(documentsApi.getDocumentUploadCapabilities).mockResolvedValue({
			max_bytes: 20 * 1024 * 1024,
			rich_extraction: true,
			accept: 'application/pdf,.pdf,image/png,.png'
		});
		vi.spyOn(documentsApi, 'listDocuments').mockResolvedValue([]);

		render(Page);

		await waitFor(() =>
			expect(screen.getByText(/PDF, images, text, web pages, office files, and e-books/i)).toBeInTheDocument()
		);
		expect(documentsApi.getDocumentUploadCapabilities).toHaveBeenCalledTimes(1);
	});
});
