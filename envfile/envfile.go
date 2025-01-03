// Package envfile parses and manipulates environment variable files ("env files").
// It supports shell-style variable substitution, quoted values, comments,
// environment merging, and conversion between string slices and its own environment map type.
package envfile

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Env represents a mapping of environment variable names to their values.
type Env map[string]string

// Keys returns a sorted slice of environment variable names.
func (e Env) Keys() []string {
	keys := []string{}

	for k := range e {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}

// Strings converts the environment map to a slice of "KEY=VALUE" strings.
func (e Env) Strings() []string {
	pairs := []string{}

	for _, k := range e.Keys() {
		pairs = append(pairs, k+"="+e[k])
	}

	return pairs
}

// EnvFromStrings creates an Env from a slice of "KEY=VALUE" strings.
func EnvFromStrings(strs []string) Env {
	env := make(Env)

	for _, s := range strs {
		split := strings.SplitN(s, "=", 2)
		key := split[0]
		value := ""
		if len(split) == 2 {
			value = split[1]
		}

		env[key] = value
	}

	return env
}

// Parse reads environment variables from an io.Reader and returns them as a map.
// If subst is true, it substitutes variables from the same env file and substEnv.
func Parse(r io.Reader, subst bool, substEnv Env) (Env, error) {
	if substEnv == nil {
		substEnv = make(Env)
	}

	env := make(Env)
	lookupEnv := func(key string) (string, error) {
		if val, exists := env[key]; exists {
			return val, nil
		}

		if val, exists := substEnv[key]; exists {
			return val, nil
		}

		return "", fmt.Errorf("can't substitute env variable: %q", key)
	}

	re := regexp.MustCompile(`\$\{([^}=]+)\}`)

	replacement := func(value string) (string, error) {
		var lastErr error

		result := re.ReplaceAllStringFunc(value, func(match string) string {
			varName := re.FindStringSubmatch(match)[1]

			subValue, err := lookupEnv(varName)
			if err != nil {
				lastErr = err
				return match
			}

			return subValue
		})
		if lastErr != nil {
			return "", lastErr
		}

		return result, nil
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse a key-value pair.
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("can't parse env file line: %q", line)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		doubleQuoted := strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`)
		singleQuoted := strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")

		// Remove the surrounding quotes and handle substitution.
		substEnabled := subst
		if len(value) > 1 && (doubleQuoted || singleQuoted) {
			if singleQuoted {
				substEnabled = false
			}

			value = value[1 : len(value)-1]
		}

		if substEnabled {
			var err error
			value, err = replacement(value)
			if err != nil {
				return nil, fmt.Errorf("error substituting value for key %q: %w", key, err)
			}
		}

		env[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return env, nil
}

// Load reads and parses an environment file at the given path.
// If subst is true, it performs variable substitution using values from the same file and substEnv.
func Load(filePath string, subst bool, substEnv Env) (Env, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return Env{}, nil
	}
	defer f.Close()

	return Parse(f, subst, substEnv)
}

// OS returns the current process environment as an Env.
func OS() Env {
	return EnvFromStrings(os.Environ())
}

// Merge combines multiple environments into a single Env.
// Later environments override values from earlier ones.
func Merge(envs ...Env) Env {
	merged := make(Env)

	for _, env := range envs {
		maps.Copy(merged, env)
	}

	return merged
}
