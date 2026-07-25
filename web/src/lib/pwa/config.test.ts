import { describe, expect, it } from 'vitest';
import config from '../../../svelte.config.js';

describe('service worker asset selection', () => {
	it('ships runtime identity assets without precaching the source master', () => {
		const include = config.kit?.serviceWorker?.files;
		expect(include).toBeTypeOf('function');
		expect(include?.('icons/icon-192.png')).toBe(true);
		expect(include?.('icons/kadence-master.png')).toBe(false);
	});
});
