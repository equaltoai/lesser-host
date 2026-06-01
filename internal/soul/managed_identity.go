package soul

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const ManagedENSRootName = "lessersoul.eth"

var managedHandleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)

// managedInstanceSlugRE intentionally mirrors controlplane.instanceSlugRE.
var managedInstanceSlugRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

// ValidateManagedHandle validates future managed identity handles shared across
// ENS labels, ActivityPub paths/handles, email local-part derivation, and URL
// paths.
//
// Grammar: 3-63 ASCII lowercase letters, digits, or hyphens; the first and last
// character must be alphanumeric. The validator intentionally does not
// lowercase, trim, strip "@", or otherwise translate input.
func ValidateManagedHandle(raw string) (string, error) {
	handle := strings.TrimSpace(raw)
	if handle == "" {
		return "", errors.New("managed handle is required")
	}
	if raw != handle {
		return "", errors.New("managed handle must not contain leading or trailing whitespace")
	}
	if !managedHandleRE.MatchString(handle) {
		return "", errors.New("managed handle must be 3-63 lowercase letters, digits, or hyphens and start/end with a letter or digit")
	}
	return handle, nil
}

// ValidateManagedInstanceSlug applies the managed-instance slug grammar used by
// host's provisioning paths. It intentionally mirrors the existing instance slug
// rule without adding a migration-time translation layer.
func ValidateManagedInstanceSlug(raw string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(raw))
	if slug == "" {
		return "", errors.New("managed instance slug is required")
	}
	if !managedInstanceSlugRE.MatchString(slug) {
		return "", errors.New("managed instance slug must be lowercase letters, digits, or hyphens and start/end with a letter or digit")
	}
	return slug, nil
}

// ManagedENSName derives the canonical managed ENS name for a hosted identity.
// The canonical form is exactly:
//
//	<name>.<instance-slug>.lessersoul.eth
func ManagedENSName(name string, instanceSlug string) (string, error) {
	handle, err := ValidateManagedHandle(name)
	if err != nil {
		return "", err
	}
	slug, err := ValidateManagedInstanceSlug(instanceSlug)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s.%s", handle, slug, ManagedENSRootName), nil
}

// LegacyBareManagedENSNameForMigration derives the pre-instance-scoped managed
// ENS name that host emitted before managed ENS names included the instance
// slug. It exists only so migration paths can recognize and replace known
// legacy managed state without treating arbitrary external ENS channels as
// host-managed.
func LegacyBareManagedENSNameForMigration(name string) (string, error) {
	handle, err := ValidateManagedHandle(name)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s.%s", handle, ManagedENSRootName), nil
}
