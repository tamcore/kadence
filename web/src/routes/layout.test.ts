import { render, screen, waitFor } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { readable } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';

const getCurrentUserMock = vi.fn();
const gotoMock = vi.fn();
const afterNavigateMock = vi.fn();

vi.mock('$app/stores', () => ({
	page: readable({
		url: new URL('https://kadence.example/login'),
		params: {}
	})
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

vi.mock('$lib/api/context', () => ({ getOverview: vi.fn() }));
vi.mock('$lib/api/mcp', () => ({ listMcp: vi.fn() }));

import Layout from './+layout.svelte';

afterEach(() => {
	vi.clearAllMocks();
	Object.defineProperty(navigator, 'onLine', { configurable: true, value: true });
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
});
