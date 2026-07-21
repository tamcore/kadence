import { api } from '$lib/api/client';
import type { User } from '$lib/types';
import { bufferToBase64url, base64urlToBuffer } from './base64url';

export interface Passkey {
	publicId: string;
	name: string;
	createdAt: string;
	lastUsedAt: string | null;
}

interface EncodedCredentialDescriptor {
	id: string;
	type: string;
	transports?: string[];
}

interface EncodedCreationOptions {
	publicKey: {
		challenge: string;
		user: { id: string; name: string; displayName: string };
		excludeCredentials?: EncodedCredentialDescriptor[];
		[key: string]: unknown;
	};
}

interface EncodedRequestOptions {
	publicKey: {
		challenge: string;
		allowCredentials?: EncodedCredentialDescriptor[];
		[key: string]: unknown;
	};
}

export async function isWebAuthnEnabled(): Promise<boolean> {
	try {
		const result = await api.get<{ enabled: boolean }>('/webauthn/enabled');
		return !!result?.enabled;
	} catch {
		return false;
	}
}

export function listPasskeys(): Promise<Passkey[]> {
	return api.get<Passkey[]>('/webauthn/credentials');
}

export function renamePasskey(publicId: string, name: string): Promise<unknown> {
	return api.patch(`/webauthn/credentials/${encodeURIComponent(publicId)}`, { name });
}

export function deletePasskey(publicId: string): Promise<unknown> {
	return api.del(`/webauthn/credentials/${encodeURIComponent(publicId)}`);
}

function decodeCreationOptions(opts: EncodedCreationOptions): CredentialCreationOptions {
	const publicKey = opts.publicKey;
	return {
		publicKey: {
			...publicKey,
			challenge: base64urlToBuffer(publicKey.challenge),
			user: {
				...publicKey.user,
				id: base64urlToBuffer(publicKey.user.id)
			},
			excludeCredentials: publicKey.excludeCredentials?.map((cred) => ({
				...cred,
				id: base64urlToBuffer(cred.id)
			})) as PublicKeyCredentialDescriptor[] | undefined
		} as PublicKeyCredentialCreationOptions
	};
}

function decodeRequestOptions(opts: EncodedRequestOptions): CredentialRequestOptions {
	const publicKey = opts.publicKey;
	return {
		publicKey: {
			...publicKey,
			challenge: base64urlToBuffer(publicKey.challenge),
			allowCredentials: publicKey.allowCredentials?.map((cred) => ({
				...cred,
				id: base64urlToBuffer(cred.id)
			})) as PublicKeyCredentialDescriptor[] | undefined
		} as PublicKeyCredentialRequestOptions
	};
}

function attestationToJSON(cred: PublicKeyCredential) {
	const response = cred.response as AuthenticatorAttestationResponse;
	return {
		id: cred.id,
		rawId: bufferToBase64url(cred.rawId),
		type: cred.type,
		response: {
			attestationObject: bufferToBase64url(response.attestationObject),
			clientDataJSON: bufferToBase64url(response.clientDataJSON)
		}
	};
}

function assertionToJSON(cred: PublicKeyCredential) {
	const response = cred.response as AuthenticatorAssertionResponse;
	return {
		id: cred.id,
		rawId: bufferToBase64url(cred.rawId),
		type: cred.type,
		response: {
			authenticatorData: bufferToBase64url(response.authenticatorData),
			clientDataJSON: bufferToBase64url(response.clientDataJSON),
			signature: bufferToBase64url(response.signature),
			userHandle: response.userHandle ? bufferToBase64url(response.userHandle) : null
		}
	};
}

export async function registerPasskey(name: string): Promise<void> {
	const options = await api.post<EncodedCreationOptions>('/webauthn/register/begin', {});
	const cred = (await navigator.credentials.create(decodeCreationOptions(options))) as PublicKeyCredential | null;
	if (!cred) throw new Error('registration cancelled');
	await api.post(`/webauthn/register/finish?name=${encodeURIComponent(name)}`, attestationToJSON(cred));
}

export async function loginWithPasskey(): Promise<User> {
	const options = await api.post<EncodedRequestOptions>('/webauthn/login/begin', {});
	const cred = (await navigator.credentials.get(decodeRequestOptions(options))) as PublicKeyCredential | null;
	if (!cred) throw new Error('login cancelled');
	return api.post<User>('/webauthn/login/finish', assertionToJSON(cred));
}
