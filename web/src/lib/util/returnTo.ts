// sanitizeReturnTo returns a safe same-site path or "/" — prevents open redirects.
export function sanitizeReturnTo(raw: string | null | undefined): string {
	if (!raw) return '/';
	// Must be a rooted path, and NOT protocol-relative ("//host").
	if (!raw.startsWith('/') || raw.startsWith('//')) return '/';
	return raw;
}
