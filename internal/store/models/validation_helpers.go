package models

import (
	"fmt"
	"strings"
)

func requireNonEmpty(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

func requirePositiveInt(field string, value int) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", field)
	}
	return nil
}

func requireOneOf(field string, value string, allowed ...string) error {
	value = strings.TrimSpace(value)
	for _, a := range allowed {
		if value == strings.TrimSpace(a) {
			return nil
		}
	}
	return fmt.Errorf("%s is invalid", field)
}
