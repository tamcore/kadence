export function bufferToBase64url(buf: ArrayBuffer): string {
	const bytes = new Uint8Array(buf);
	let binary = '';
	for (const byte of bytes) binary += String.fromCharCode(byte);
	return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function base64urlToBuffer(value: string): ArrayBuffer {
	const pad = value.length % 4 === 0 ? '' : '='.repeat(4 - (value.length % 4));
	const base64 = value.replace(/-/g, '+').replace(/_/g, '/') + pad;
	const binary = atob(base64);
	const bytes = new Uint8Array(binary.length);
	for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
	return bytes.buffer;
}
