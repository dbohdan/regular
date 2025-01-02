package shellquote

import "testing"

func TestQuote(t *testing.T) {
	tests := []struct {
		input    string
		shell    string
		expected string
		wantErr  bool
	}{
		{"hello", "fish", "hello", false},
		{"hello world", "fish", "'hello world'", false},
		{"it's", "fish", `'it\'s'`, false},
		{"complex'quote", "fish", `'complex\'quote'`, false},

		{"hello", "posix", "hello", false},
		{"hello world", "posix", "'hello world'", false},
		{"it's", "posix", `'it'"'"'s'`, false},
		{"complex'quote", "posix", `'complex'"'"'quote'`, false},

		{"hello", "invalid", "", true},
	}

	for _, tt := range tests {
		got, err := Quote(tt.input, tt.shell)
		if (err != nil) != tt.wantErr {
			t.Errorf("Quote(%q, %q) error = %v, wantErr %v",
				tt.input, tt.shell, err, tt.wantErr)
			continue
		}

		if got != tt.expected {
			t.Errorf("Quote(%q, %q) = %q, want %q",
				tt.input, tt.shell, got, tt.expected)
		}
	}
}

func TestShellSafe(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"hello", true},
		{"hello123", true},
		{"hello-world", true},
		{"hello_world", true},
		{"hello.world", true},
		{"hello world", false},
		{"hello'world", false},
		{"hello\"world", false},
		{"hello$world", false},
		{"hello\nworld", false},
	}

	for _, tt := range tests {
		got := shellSafe(tt.input)
		if got != tt.expected {
			t.Errorf("shellSafe(%q) = %v, want %v",
				tt.input, got, tt.expected)
		}
	}
}
