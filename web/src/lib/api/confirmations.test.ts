import { describe, it, expect, vi, beforeEach } from 'vitest';
import { submitConfirmation } from '$lib/api/confirmations';
import { api } from '$lib/api/client';

vi.mock('$lib/api/client', () => ({
	api: { post: vi.fn() }
}));

describe('submitConfirmation', () => {
	beforeEach(() => {
		vi.mocked(api.post).mockReset();
		vi.mocked(api.post).mockResolvedValue(undefined);
	});

	it('posts an allow to the confirmation endpoint', async () => {
		await submitConfirmation('req-1', true);

		expect(api.post).toHaveBeenCalledWith('/confirmations/req-1', { confirm: true });
	});

	it('posts a decline as confirm false', async () => {
		await submitConfirmation('req-1', false);

		expect(api.post).toHaveBeenCalledWith('/confirmations/req-1', { confirm: false });
	});

	it('escapes the request id so it cannot alter the path', async () => {
		await submitConfirmation('a/../b', true);

		expect(api.post).toHaveBeenCalledWith('/confirmations/a%2F..%2Fb', { confirm: true });
	});
});
