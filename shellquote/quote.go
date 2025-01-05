package shellquote

import (
	"fmt"
	"regexp"
	"strings"
)

// Returns a version of the string quoted for the given shell interpreter.
func Quote(s string, shell string) (string, error) {
	switch shell {

	case "fish":
		return Fish(s), nil

	case "posix":
		return Posix(s), nil

	default:
		return "", fmt.Errorf("unsupported shell: %s", shell)
	}
}

func Fish(s string) string {
	if shellSafe(s) {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `\'`) + "'"
}

func Posix(s string) string {
	if shellSafe(s) {
		return s
	}

	// Simple POSIX shell quoting: wrap in single quotes and escape single quotes.
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func shellSafe(s string) bool {
	re := regexp.MustCompile("^[A-Za-z0-9%+,-./:=@_]+$")
	return re.MatchString(s)
}
