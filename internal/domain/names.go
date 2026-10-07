package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)
	vesselCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{2,31}$`)
	runCodePattern    = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]{3,47}$`)
)

func NewID(prefix string) (string, error) {
	buffer := make([]byte, 10)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(buffer), nil
}

func NormalizeName(value string) string {
	fields := strings.Fields(strings.TrimSpace(value))
	return strings.Join(fields, " ")
}

func NameKey(value string) string {
	var builder strings.Builder
	for _, r := range NormalizeName(value) {
		builder.WriteRune(unicode.ToLower(r))
	}
	return builder.String()
}

func ValidateIdentifier(value, field string) error {
	if !identifierPattern.MatchString(value) {
		return FieldError(CodeInvalidInput, field,
			"must start with a lowercase letter and contain 3 to 64 lowercase letters, digits, or hyphens")
	}
	return nil
}

func NormalizeVesselCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func ValidateVesselCode(value, field string) error {
	if !vesselCodePattern.MatchString(value) {
		return FieldError(CodeInvalidInput, field,
			"must contain 3 to 32 uppercase letters, digits, or hyphens")
	}
	return nil
}

func NormalizeRunCode(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func ValidateRunCode(value, field string) error {
	if !runCodePattern.MatchString(value) {
		return FieldError(CodeInvalidInput, field,
			"must contain 4 to 48 uppercase letters, digits, or hyphens")
	}
	return nil
}
