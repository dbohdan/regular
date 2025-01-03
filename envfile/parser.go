package envfile

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

type parser struct {
	env      Env
	reader   io.Reader
	subst    bool
	substEnv Env
}

const (
	whitespace = " \t"
)

func newParser(r io.Reader, subst bool, substEnv Env) *parser {
	return &parser{
		env:    make(Env),
		reader: r,
		subst:  subst,
		// Make a copy of substEnv.
		substEnv: Merge(substEnv),
	}
}

func (p *parser) parse() (Env, error) {
	scanner := bufio.NewScanner(p.reader)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		line = strings.TrimLeft(line, whitespace)

		// Skip full-line comments and empty lines.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Handle the "export" prefix.
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimPrefix(line, "export ")
		}

		// Split at the first equals sign.
		pos := strings.Index(line, "=")
		if pos == -1 {
			return nil, fmt.Errorf("line %d: no equals sign", lineNum)
		}

		varName := strings.Trim(line[:pos], whitespace)
		if varName == "" {
			return nil, fmt.Errorf("line %d: empty variable name", lineNum)
		}

		rawValue := strings.TrimLeft(line[pos+1:], whitespace)
		value := rawValue

		// Handle quoted and unquoted values.
		if rawValue != "" && (rawValue[0] == '"' || rawValue[0] == '\'') {
			openingLineNum := lineNum
			quote := rawValue[0]

			if !hasUnescapedEndQuote(rawValue[1:], quote) {
				// Handle a multiline quoted value.
				var builder strings.Builder

				builder.WriteString(rawValue)
				sawEndQuote := false

				for scanner.Scan() {
					lineNum++
					nextLine := scanner.Text()

					builder.WriteString("\n")
					builder.WriteString(nextLine)

					if hasUnescapedEndQuote(nextLine, quote) {
						sawEndQuote = true
						break
					}
				}

				if !sawEndQuote {
					return nil, fmt.Errorf("line %d: reached end looking for closing quote", openingLineNum)
				}

				value = builder.String()
			}
		}

		commentStart := findCommentStart(value)
		if commentStart > -1 {
			value = value[:commentStart]
		}
		value = strings.Trim(value, whitespace)

		parsedValue, err := p.parseValue(value)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", lineNum, err)
		}

		p.env[varName] = parsedValue
	}

	return p.env, scanner.Err()
}

func hasUnescapedEndQuote(s string, quote byte) bool {
	escaped := false

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && !escaped {
			escaped = true
			continue
		}

		if s[i] == quote && !escaped {
			return true
		}

		escaped = false
	}

	return false
}

// findCommentStart locates the first valid comment marker `#`.
func findCommentStart(value string) int {
	escaped := false

	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && !escaped {
			escaped = true
			continue
		}

		if value[i] == '#' && !escaped {
			if i == 0 {
				return i
			}

			if i > 0 && (value[i-1] == '"' || strings.ContainsRune(whitespace, rune(value[i-1]))) {
				return i
			}
		}

		escaped = false
	}

	return -1
}

func (p *parser) parseValue(value string) (string, error) {
	if value == "" {
		return "", nil
	}

	// Handle quoted values.
	if len(value) >= 2 {
		if value[0] == '"' && strings.HasSuffix(value, "\"") {
			unquoted := value[1 : len(value)-1]
			return p.expandValue(unquoted)
		}

		if value[0] == '\'' && strings.HasSuffix(value, "'") {
			unquoted := value[1 : len(value)-1]
			return p.expandSingleQuoted(unquoted)
		}
	}

	// Unquoted value.
	return p.expandValue(value)
}

func (p *parser) expandSingleQuoted(value string) (string, error) {
	var result strings.Builder
	escaped := false

	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && !escaped {
			escaped = true
			continue
		}

		if escaped {
			result.WriteByte('\\')
			result.WriteByte(value[i])

			escaped = false
		} else {
			result.WriteByte(value[i])
		}
	}

	return result.String(), nil
}

func (p *parser) expandValue(value string) (string, error) {
	var result strings.Builder
	escaped := false

	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && !escaped {
			escaped = true
			continue
		}

		if escaped {
			switch value[i] {

			case 'n':
				result.WriteByte('\n')

			case 'r':
				result.WriteByte('\r')

			case 't':
				result.WriteByte('\t')

			case '"':
				result.WriteByte('"')

			case '\\':
				result.WriteByte('\\')

			default:
				result.WriteByte('\\')
				result.WriteByte(value[i])
			}

			escaped = false
			continue
		}

		if p.subst && !escaped && value[i] == '$' && i < len(value)-1 {
			varName, offset, err := p.extractVarName(value[i:])
			if err != nil {
				return "", err
			}

			replacement, err := p.getValue(varName)
			if err != nil {
				return "", err
			}

			result.WriteString(replacement)

			i += offset
			continue
		}

		result.WriteByte(value[i])
	}

	return result.String(), nil
}

func (p *parser) extractVarName(value string) (string, int, error) {
	if len(value) < 2 || value[0] != '$' {
		return "", 0, fmt.Errorf("invalid variable substitution syntax")
	}

	if value[1] == '{' {
		// Handle "${VAR}" syntax.
		endIdx := strings.IndexByte(value, '}')
		if endIdx == -1 {
			return "", 0, fmt.Errorf("unclosed variable substitution")
		}

		return value[2:endIdx], endIdx, nil
	}

	// Handle "$VAR" syntax.
	var endIdx int
	for endIdx = 1; endIdx < len(value); endIdx++ {
		if !isVarNameCharacter(value[endIdx]) {
			break
		}
	}

	return value[1:endIdx], endIdx - 1, nil
}

func isVarNameCharacter(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_'
}

func (p *parser) getValue(varName string) (string, error) {
	value, ok := p.env[varName]
	if ok {
		return value, nil
	}

	value, ok = p.substEnv[varName]
	if ok {
		return value, nil
	}

	return "", fmt.Errorf("unknown variable: %v", varName)
}
