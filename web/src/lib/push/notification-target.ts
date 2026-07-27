export function resolveClientToFocus(clients: { url: string }[], targetPath: string): number {
	const target = targetPath.split('#')[0];
	return clients.findIndex((c) => new URL(c.url).pathname === target);
}
