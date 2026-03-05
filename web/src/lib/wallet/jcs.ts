export type JcsValue = null | boolean | number | string | JcsValue[] | { [key: string]: JcsValue };

function isJsonObject(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === 'object' && !Array.isArray(value);
}

function canonicalize(value: unknown): string {
	if (value === null) return 'null';

	switch (typeof value) {
		case 'boolean':
			return value ? 'true' : 'false';
		case 'number':
			if (!Number.isFinite(value)) return 'null';
			return JSON.stringify(value);
		case 'string':
			return JSON.stringify(value);
		case 'object':
			break;
		default:
			throw new Error(`Unsupported value type for JCS: ${typeof value}`);
	}

	if (Array.isArray(value)) {
		return `[${value.map((v) => canonicalize(v)).join(',')}]`;
	}

	if (!isJsonObject(value)) {
		throw new Error('Unsupported JSON value');
	}

	const keys = Object.keys(value).sort();
	const parts: string[] = [];
	for (const key of keys) {
		const v = (value as Record<string, unknown>)[key];
		if (v === undefined) continue;
		parts.push(`${JSON.stringify(key)}:${canonicalize(v)}`);
	}
	return `{${parts.join(',')}}`;
}

export function jcsCanonicalize(value: unknown): string {
	return canonicalize(value);
}
