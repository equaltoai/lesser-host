package models

import (
	"os"
	"strings"
)

const (
	SKMetadata = "METADATA"
	SKProfile  = "PROFILE"
)

const (
	KeyPatternUser    = "USER#%s"
	KeyPatternSession = "SESSION#%s"
)

func MainTableName() string {
	name := strings.TrimSpace(os.Getenv("STATE_TABLE_NAME"))
	if name == "" {
		return "lesser-host-state"
	}
	return name
}

