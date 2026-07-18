import { describe, expect, it } from 'vitest';
import { sanitizeReturnTo } from './returnTo';

describe('sanitizeReturnTo', () => {
	it('allows a same-site absolute path', () => {
		expect(sanitizeReturnTo('/admin/users')).toBe('/admin/users');
	});
	it('falls back to / for null/empty', () => {
		expect(sanitizeReturnTo(null)).toBe('/');
		expect(sanitizeReturnTo('')).toBe('/');
	});
	it('rejects protocol-relative and absolute URLs (open redirect)', () => {
		expect(sanitizeReturnTo('//evil.com')).toBe('/');
		expect(sanitizeReturnTo('https://evil.com')).toBe('/');
		expect(sanitizeReturnTo('javascript:alert(1)')).toBe('/');
	});
	it('rejects a path not starting with /', () => {
		expect(sanitizeReturnTo('admin')).toBe('/');
	});
});
