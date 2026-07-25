export type CacheStrategy = 'ignore' | 'precache' | 'navigation' | 'network';

export interface RequestDescriptor {
	method: string;
	mode: RequestMode;
	url: string;
}

export function classifyRequest(
	request: RequestDescriptor,
	origin: string,
	precachedPaths: ReadonlySet<string>
): CacheStrategy {
	if (request.method !== 'GET') return 'ignore';

	const url = new URL(request.url);
	if (url.origin !== origin) return 'ignore';
	if (
		url.pathname === '/api' ||
		url.pathname.startsWith('/api/') ||
		url.pathname === '/service-worker.js'
	) {
		return 'network';
	}
	if (precachedPaths.has(url.pathname)) return 'precache';
	if (request.mode === 'navigate') return 'navigation';
	return 'network';
}
