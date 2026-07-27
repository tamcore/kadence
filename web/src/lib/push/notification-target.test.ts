import { describe, it, expect } from 'vitest';
import { resolveClientToFocus } from './notification-target';

describe('resolveClientToFocus', () => {
	it('matches an open client by path ignoring hash', () => {
		const clients = [{ url: 'https://app/settings' }, { url: 'https://app/chat/abc' }];
		expect(resolveClientToFocus(clients, '/chat/abc#msg=5')).toBe(1);
	});
	it('returns -1 when none match', () => {
		expect(resolveClientToFocus([{ url: 'https://app/x' }], '/chat/abc')).toBe(-1);
	});
});
