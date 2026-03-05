import { keccak_256 } from '@noble/hashes/sha3.js';

function bytesToHex(bytes: Uint8Array): string {
	let out = '';
	for (const b of bytes) {
		out += b.toString(16).padStart(2, '0');
	}
	return out;
}

export function keccak256Utf8Hex(message: string): string {
	const enc = new TextEncoder();
	const digest = keccak_256(enc.encode(message));
	return `0x${bytesToHex(digest)}`;
}
