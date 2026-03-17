const managedReleaseTagRE = /^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/;

export function validateManagedReleaseTag(
	value: string,
	options?: {
		allowBlank?: boolean;
		label?: string;
	}
): string | null {
	const trimmed = value.trim();
	const allowBlank = options?.allowBlank ?? false;
	const label = options?.label ?? 'Release version';

	if (!trimmed) {
		return allowBlank ? null : `${label} is required.`;
	}
	if (trimmed.toLowerCase() === 'latest') {
		return null;
	}
	if (managedReleaseTagRE.test(trimmed)) {
		return null;
	}
	return `${label} must be "latest" or a tag like v1.2.3.`;
}
