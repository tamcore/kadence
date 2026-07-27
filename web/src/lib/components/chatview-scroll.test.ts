import { describe, it, expect } from 'vitest';
import { parseMsgAnchor } from './chatview-scroll';

describe('parseMsgAnchor', () => {
	it('extracts the message id from #msg=42', () => {
		expect(parseMsgAnchor('#msg=42')).toBe(42);
	});
	it('returns null for no anchor', () => {
		expect(parseMsgAnchor('')).toBeNull();
		expect(parseMsgAnchor('#other')).toBeNull();
	});
	it('returns null for a non-numeric id', () => {
		expect(parseMsgAnchor('#msg=abc')).toBeNull();
	});
});
