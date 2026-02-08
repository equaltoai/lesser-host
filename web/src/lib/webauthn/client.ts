function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function decodeBase64Url(input: string): Uint8Array {
	const base64 = input.replace(/-/g, '+').replace(/_/g, '/');
	const padded = base64.padEnd(base64.length + ((4 - (base64.length % 4)) % 4), '=');
	const raw = atob(padded);
	const bytes = new Uint8Array(raw.length);
	for (let i = 0; i < raw.length; i++) {
		bytes[i] = raw.charCodeAt(i);
	}
	return bytes;
}

function encodeBase64Url(input: ArrayBuffer): string {
	const bytes = new Uint8Array(input);
	let binary = '';
	const chunkSize = 0x2000;
	for (let i = 0; i < bytes.length; i += chunkSize) {
		const chunk = bytes.subarray(i, i + chunkSize);
		binary += String.fromCharCode(...chunk);
	}
	return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

function decodeField(input: unknown, fieldName: string): ArrayBuffer {
	if (typeof input !== 'string' || input.trim() === '') {
		throw new Error(`invalid ${fieldName}`);
	}
	return decodeBase64Url(input).buffer as ArrayBuffer;
}

function unwrapPublicKeyOptions(input: Record<string, unknown>): Record<string, unknown> {
	if (isRecord(input.publicKey)) {
		return input.publicKey as Record<string, unknown>;
	}
	return input;
}

export function toPublicKeyCreationOptions(
	publicKey: Record<string, unknown>,
): PublicKeyCredentialCreationOptions {
	const root = unwrapPublicKeyOptions(publicKey);
	const out: Record<string, unknown> = { ...root };
	out.challenge = decodeField(root.challenge, 'challenge');

	if (isRecord(root.user)) {
		out.user = {
			...root.user,
			id: decodeField(root.user.id, 'user.id'),
		};
	}

	if (Array.isArray(root.excludeCredentials)) {
		out.excludeCredentials = root.excludeCredentials.map((cred) => {
			if (!isRecord(cred)) return cred;
			return {
				...cred,
				id: decodeField(cred.id, 'excludeCredentials.id'),
			};
		});
	}

	return out as unknown as PublicKeyCredentialCreationOptions;
}

export function toPublicKeyRequestOptions(publicKey: Record<string, unknown>): PublicKeyCredentialRequestOptions {
	const root = unwrapPublicKeyOptions(publicKey);
	const out: Record<string, unknown> = { ...root };
	out.challenge = decodeField(root.challenge, 'challenge');

	if (Array.isArray(root.allowCredentials)) {
		out.allowCredentials = root.allowCredentials.map((cred) => {
			if (!isRecord(cred)) return cred;
			return {
				...cred,
				id: decodeField(cred.id, 'allowCredentials.id'),
			};
		});
	}

	return out as unknown as PublicKeyCredentialRequestOptions;
}

export function serializeCredentialCreation(credential: PublicKeyCredential): Record<string, unknown> {
	const response = credential.response as AuthenticatorAttestationResponse;
	const transports =
		typeof (response as unknown as { getTransports?: () => string[] }).getTransports === 'function'
			? (response as unknown as { getTransports: () => string[] }).getTransports()
			: undefined;

	return {
		id: credential.id,
		rawId: encodeBase64Url(credential.rawId),
		type: credential.type,
		authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
		response: {
			attestationObject: encodeBase64Url(response.attestationObject),
			clientDataJSON: encodeBase64Url(response.clientDataJSON),
			transports,
		},
		clientExtensionResults: credential.getClientExtensionResults(),
	};
}

export function serializeCredentialRequest(credential: PublicKeyCredential): Record<string, unknown> {
	const response = credential.response as AuthenticatorAssertionResponse;
	return {
		id: credential.id,
		rawId: encodeBase64Url(credential.rawId),
		type: credential.type,
		authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
		response: {
			authenticatorData: encodeBase64Url(response.authenticatorData),
			clientDataJSON: encodeBase64Url(response.clientDataJSON),
			signature: encodeBase64Url(response.signature),
			userHandle: response.userHandle ? encodeBase64Url(response.userHandle) : undefined,
		},
		clientExtensionResults: credential.getClientExtensionResults(),
	};
}
