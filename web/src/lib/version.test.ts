import { describe, expect, it } from 'vitest';
import { APP_NAME } from './version';

describe('version', () => {
	it('exposes the app name', () => {
		expect(APP_NAME).toBe('Kadence');
	});
});
