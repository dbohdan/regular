package starlarkutil

import (
	"testing"

	"go.starlark.net/starlark"
)

func TestAddPredeclared(t *testing.T) {
	d := starlark.StringDict{}
	AddPredeclared(d)

	if _, ok := d["quote"]; !ok {
		t.Error("quote function not added to predeclared dict")
	}
}

func TestQuote(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		shell    string
		expected string
		wantErr  bool
	}{
		{
			name:     "simple posix quote",
			input:    "hello world",
			shell:    "posix",
			expected: "'hello world'",
			wantErr:  false,
		},
		{
			name:     "simple fish quote",
			input:    "hello world",
			shell:    "fish",
			expected: "'hello world'",
			wantErr:  false,
		},
		{
			name:     "posix quote with single quote",
			input:    "don't",
			shell:    "posix",
			expected: `'don'"'"'t'`,
			wantErr:  false,
		},
		{
			name:     "fish quote with single quote",
			input:    "don't",
			shell:    "fish",
			expected: `'don\'t'`,
			wantErr:  false,
		},
		{
			name:     "invalid shell",
			input:    "test",
			shell:    "invalid",
			expected: "",
			wantErr:  true,
		},
	}

	thread := &starlark.Thread{Name: "test"}
	builtin := starlark.NewBuiltin("quote", Quote)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := starlark.Tuple{starlark.String(tt.input)}
			if tt.shell != "posix" {
				args = append(args, starlark.String(tt.shell))
			}

			got, err := Quote(thread, builtin, args, nil)

			if (err != nil) != tt.wantErr {
				t.Errorf("Quote() error = %q, wantErr %v", err, tt.wantErr)
				return
			}

			gotStr, ok := got.(starlark.String)
			if !ok {
				t.Errorf("Quote() return value doesn't Starlark string")
				return
			}

			if !tt.wantErr {
				if gotStr.GoString() != tt.expected {
					t.Errorf("Quote() = %q, want %q", got, tt.expected)
				}
			}
		})
	}
}
