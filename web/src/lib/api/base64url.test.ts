import { describe, it, expect } from 'vitest';
import { bufferToBase64url, base64urlToBuffer } from './base64url';

describe('base64url', () => {
	it('round-trips arbitrary bytes', () => {
		const bytes = new Uint8Array([0, 1, 2, 250, 251, 255, 62, 63]);
		const s = bufferToBase64url(bytes.buffer);
		expect(s).not.toContain('+');
		expect(s).not.toContain('/');
		expect(s).not.toContain('=');
		expect(new Uint8Array(base64urlToBuffer(s))).toEqual(bytes);
	});
});
