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

type Env map[string]string

func (e Env) Keys() []string {
	keys := []string{}

	for k := range e {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}

func (e Env) Strings() []string {
	pairs := []string{}

	for _, k := range e.Keys() {
		pairs = append(pairs, k+"="+e[k])
	}

	return pairs
}

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

// `Parse` reads environment variables from a reader and returns them as a map.
// If `subst` is true, it substitutes variables from the same env file and `substEnv`.
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

func Load(filePath string, subst bool, substEnv Env) (Env, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return Env{}, nil
	}
	defer f.Close()

	return Parse(f, subst, substEnv)
}

func OS() Env {
	return EnvFromStrings(os.Environ())
}

func Merge(envs ...Env) Env {
	merged := make(Env)

	for _, env := range envs {
		maps.Copy(merged, env)
	}

	return merged
}
