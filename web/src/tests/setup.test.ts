import { describe, expect, it, vi } from 'vitest';

describe('test harness matchMedia polyfill', () => {
	it('exposes matchMedia with the queried media string', () => {
		const mq = window.matchMedia('(prefers-color-scheme: dark)');
		expect(mq.media).toBe('(prefers-color-scheme: dark)');
		expect(typeof mq.matches).toBe('boolean');
	});

	it('supports modern and legacy listener registration', () => {
		const mq = window.matchMedia('(prefers-color-scheme: dark)');
		const listener = vi.fn();
		expect(() => mq.addEventListener('change', listener)).not.toThrow();
		expect(() => mq.removeEventListener('change', listener)).not.toThrow();
		expect(() => mq.addListener(listener)).not.toThrow();
		expect(() => mq.removeListener(listener)).not.toThrow();
	});

	it('defaults to light so existing tests are unaffected', () => {
		expect(window.matchMedia('(prefers-color-scheme: dark)').matches).toBe(false);
	});
});
