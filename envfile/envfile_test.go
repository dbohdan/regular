package envfile

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		subst    bool
		substEnv Env
		want     Env
		wantErr  bool
	}{
		{
			name:  "basic key-value pairs",
			input: "FOO=bar\nBAZ= qux ",
			want:  Env{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name:  "empty values",
			input: "EMPTY=\nKEY=value",
			want:  Env{"EMPTY": "", "KEY": "value"},
		},
		{
			name:  "quoted values",
			input: `QUOTED="hello world"` + "\nSINGLE =  'no subst ${VAR}'  ",
			subst: false,
			want:  Env{"QUOTED": "hello world", "SINGLE": "no subst ${VAR}"},
		},
		{
			name:  "substitution, braces",
			input: "BASE=/opt\nPATH=${BASE}/bin",
			subst: true,
			want:  Env{"BASE": "/opt", "PATH": "/opt/bin"},
		},
		{
			name:  "substitution, no braces",
			input: "BASE=/opt\nPATH=$BASE/bin",
			subst: true,
			want:  Env{"BASE": "/opt", "PATH": "/opt/bin"},
		},
		{
			name:     "substitution from external env",
			input:    "PATH=${HOME}/bin",
			subst:    true,
			substEnv: Env{"HOME": "/home/user"},
			want:     Env{"PATH": "/home/user/bin"},
		},
		{
			name:    "invalid substitution",
			input:   "PATH=${UNDEFINED}/bin",
			subst:   true,
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "INVALID",
			wantErr: true,
		},
		{
			name:  "comments and empty lines",
			input: "# comment\n\nKEY=value\n\n# another comment",
			want:  Env{"KEY": "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.input), tt.subst, tt.substEnv)

			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %q, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if equal, diffs := mapsEqual(got, tt.want); !equal {
					t.Errorf("Parse() got different values for keys %q\ngot: %q\nwant: %q", diffs, got, tt.want)
				}
			}
		})
	}
}

func TestEnvFromStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  Env
	}{
		{
			name:  "basic conversion",
			input: []string{"FOO=bar", "BAZ=qux"},
			want:  Env{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name:  "empty values",
			input: []string{"EMPTY=", "KEY=value"},
			want:  Env{"EMPTY": "", "KEY": "value"},
		},
		{
			name:  "no equals sign",
			input: []string{"NOVALUE"},
			want:  Env{"NOVALUE": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnvFromStrings(tt.input)
			if equal, diffs := mapsEqual(got, tt.want); !equal {
				t.Errorf("EnvFromStrings() got different values for keys %q\ngot: %v\nwant: %v", diffs, got, tt.want)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	tests := []struct {
		name string
		envs []Env
		want Env
	}{
		{
			name: "basic merge",
			envs: []Env{
				{"FOO": "bar"},
				{"BAZ": "qux"},
			},
			want: Env{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name: "override values",
			envs: []Env{
				{"FOO": "bar", "COMMON": "first"},
				{"BAZ": "qux", "COMMON": "second"},
			},
			want: Env{"FOO": "bar", "BAZ": "qux", "COMMON": "second"},
		},
		{
			name: "empty env",
			envs: []Env{
				{"FOO": "bar"},
				{},
				{"BAZ": "qux"},
			},
			want: Env{"FOO": "bar", "BAZ": "qux"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Merge(tt.envs...)
			if equal, diffs := mapsEqual(got, tt.want); !equal {
				t.Errorf("Merge() got different values for keys %q\ngot: %q\nwant: %v", diffs, got, tt.want)
			}
		})
	}
}

func TestKeys(t *testing.T) {
	env := Env{
		"FOO": "bar",
		"BAZ": "qux",
	}

	got := env.Keys()
	if len(got) != 2 {
		t.Errorf("Keys() returned %d items, want 2", len(got))
	}

	want := map[string]bool{
		"FOO": true,
		"BAZ": true,
	}

	for _, s := range got {
		if !want[s] {
			t.Errorf("Keys() unexpected value: %s", s)
		}
	}
}

func TestEnvStrings(t *testing.T) {
	env := Env{
		"FOO": "bar",
		"BAZ": "qux",
	}

	got := env.Strings()
	if len(got) != 2 {
		t.Errorf("Strings() returned %d items, want 2", len(got))
	}

	want := map[string]bool{
		"FOO=bar": true,
		"BAZ=qux": true,
	}

	for _, s := range got {
		if !want[s] {
			t.Errorf("Strings() unexpected value: %s", s)
		}
	}
}

func mapsEqual(a, b Env) (bool, []string) {
	diffs := []string{}

	for k, v := range a {
		if bv, ok := b[k]; ok && bv != v {
			diffs = append(diffs, k)
		}
	}

	return len(diffs) == 0, diffs
}
