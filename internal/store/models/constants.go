package models

import (
	"os"
	"strings"
)

// SK* constants are shared sort key values.
const (
	SKMetadata = "METADATA"
	SKProfile  = "PROFILE"
)

const walletTypeEthereum = "ethereum"

// KeyPattern* constants define common partition key patterns.
const (
	KeyPatternUser    = "USER#%s"
	KeyPatternSession = "SESSION#%s"
)

// MainTableName returns the configured database table name.
func MainTableName() string {
	name := strings.TrimSpace(os.Getenv("STATE_TABLE_NAME"))
	if name == "" {
		return "lesser-host-state"
	}
	return name
}
