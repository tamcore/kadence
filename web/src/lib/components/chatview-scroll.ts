export function parseMsgAnchor(hash: string): number | null {
	const m = /^#msg=(\d+)$/.exec(hash);
	if (!m) return null;
	const id = Number(m[1]);
	return Number.isFinite(id) ? id : null;
}
