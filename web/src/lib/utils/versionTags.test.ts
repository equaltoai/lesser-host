import { describe, expect, it } from 'vitest';

import { validateManagedReleaseTag } from './versionTags';

describe('validateManagedReleaseTag', () => {
	it('accepts latest and semver-style tags', () => {
		expect(validateManagedReleaseTag('latest')).toBeNull();
		expect(validateManagedReleaseTag('v1.2.3')).toBeNull();
		expect(validateManagedReleaseTag('v1.2.3-rc.1')).toBeNull();
		expect(validateManagedReleaseTag('v1.2.3+build.5')).toBeNull();
	});

	it('allows blank input when requested', () => {
		expect(validateManagedReleaseTag('', { allowBlank: true })).toBeNull();
	});

	it('rejects malformed tags', () => {
		expect(validateManagedReleaseTag('v.1.2.3')).toContain('must be "latest" or a tag like v1.2.3');
		expect(validateManagedReleaseTag('1.2.3')).toContain('must be "latest" or a tag like v1.2.3');
		expect(validateManagedReleaseTag('v1.2')).toContain('must be "latest" or a tag like v1.2.3');
	});
});
