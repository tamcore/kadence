import { describe, it, expect } from 'vitest';
import { urlBase64ToUint8Array } from './subscribe';

describe('urlBase64ToUint8Array', () => {
	it('decodes base64url to bytes', () => {
		const out = urlBase64ToUint8Array('AQID'); // -> [1,2,3]
		expect(Array.from(out)).toEqual([1, 2, 3]);
	});
	it('handles url-safe chars and missing padding', () => {
		const out = urlBase64ToUint8Array('-_8'); // url-safe, unpadded
		expect(out.length).toBeGreaterThan(0);
	});
});
