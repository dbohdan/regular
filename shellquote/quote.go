// Package shellquote provides functions for quoting strings for different shell interpreters.
package shellquote

import (
	"fmt"
	"regexp"
	"strings"
)

// Quote returns a version of the string quoted for the given shell interpreter.
// The value of shell must be "fish", "posix", or "powershell" (case-insensitive).
func Quote(s string, shell string) (string, error) {
	switch strings.ToLower(shell) {
	case "fish":
		return Fish(s), nil

	case "posix":
		return POSIX(s), nil

	case "powershell":
		return PowerShell(s), nil

	default:
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
}

// Fish returns a version of the string quoted for the Fish shell.
func Fish(s string) string {
	if shellSafe(s) {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

// POSIX returns a version of the string quoted for POSIX-compliant shells.
func POSIX(s string) string {
	if shellSafe(s) {
		return s
	}

	// Simple POSIX shell quoting: wrap in single quotes and escape single quotes.
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// PowerShell returns a version of the string quoted for PowerShell.
func PowerShell(s string) string {
	if shellSafe(s) {
		return s
	}

	// In PowerShell, single quotes are escaped by doubling them.
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// shellSafe reports whether the string can be used safely in shell commands without quoting.
func shellSafe(s string) bool {
	re := regexp.MustCompile("^[A-Za-z0-9%+,-./:=@_]+$")
	return re.MatchString(s)
}
