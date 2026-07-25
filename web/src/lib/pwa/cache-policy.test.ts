import { describe, expect, it } from 'vitest';
import { classifyRequest } from './cache-policy';

function request(path: string, options: { method?: string; mode?: RequestMode } = {}) {
	return {
		method: options.method ?? 'GET',
		mode: options.mode ?? 'cors',
		url: new URL(path, 'https://kadence.example').href
	};
}

describe('classifyRequest', () => {
	const origin = 'https://kadence.example';
	const precached = new Set(['/favicon.png', '/_app/immutable/app.js']);

	it('serves known app assets from the precache', () => {
		expect(classifyRequest(request('/favicon.png'), origin, precached)).toBe('precache');
		expect(classifyRequest(request('/_app/immutable/app.js'), origin, precached)).toBe('precache');
	});

	it('uses the offline shell path for same-origin navigations', () => {
		expect(classifyRequest(request('/chat/abc', { mode: 'navigate' }), origin, precached)).toBe(
			'navigation'
		);
	});

	it('leaves API and service-worker requests on the network', () => {
		expect(classifyRequest(request('/api/session'), origin, precached)).toBe('network');
		expect(classifyRequest(request('/service-worker.js'), origin, precached)).toBe('network');
	});

	it('ignores requests a Kadence worker must not intercept', () => {
		expect(classifyRequest(request('/api/chat', { method: 'POST' }), origin, precached)).toBe(
			'ignore'
		);
		expect(
			classifyRequest(
				{ method: 'GET', mode: 'cors', url: 'https://cdn.example/app.js' },
				origin,
				precached
			)
		).toBe('ignore');
	});

	it('leaves uncached same-origin GET requests on the network', () => {
		expect(classifyRequest(request('/future-static-file.txt'), origin, precached)).toBe('network');
	});
});
