import { describe, it, expect, beforeEach } from 'vitest';
import { get } from 'svelte/store';
import {
	online,
	serverReachable,
	canReachServer,
	setOnline,
	setServerReachable,
	canReachServerNow
} from './connection';

describe('connection store', () => {
	beforeEach(() => {
		setOnline(true);
		setServerReachable(true);
	});

	it('defaults to reachable', () => {
		expect(get(canReachServer)).toBe(true);
		expect(canReachServerNow()).toBe(true);
	});

	it('is unreachable when browser is offline', () => {
		setOnline(false);
		expect(get(canReachServer)).toBe(false);
		expect(canReachServerNow()).toBe(false);
	});

	it('is unreachable when server is down but browser online', () => {
		setServerReachable(false);
		expect(get(canReachServer)).toBe(false);
		expect(canReachServerNow()).toBe(false);
	});

	it('recovers when both are true again', () => {
		setOnline(false);
		setServerReachable(false);
		setOnline(true);
		setServerReachable(true);
		expect(canReachServerNow()).toBe(true);
	});
});
