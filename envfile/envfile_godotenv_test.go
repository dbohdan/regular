package envfile

// These tests are derived from GoDotEnv (https://github.com/joho/godotenv) test fixtures.
// GoDotEnv is distribured under the following license:
//
// Copyright (c) 2013 John Barton
//
// MIT License
//
// Permission is hereby granted, free of charge, to any person obtaining
// a copy of this software and associated documentation files (the
// "Software"), to deal in the Software without restriction, including
// without limitation the rights to use, copy, modify, merge, publish,
// distribute, sublicense, and/or sell copies of the Software, and to
// permit persons to whom the Software is furnished to do so, subject to
// the following conditions:
//
// The above copyright notice and this permission notice shall be
// included in all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
// EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
// MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
// NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
// LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
// OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
// WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

import (
	"io"
	"strings"
	"testing"
)

const (
	plainEnv = `OPTION_A=1
OPTION_B=2
OPTION_C= 3
OPTION_D =4
OPTION_E = 5
OPTION_F =
OPTION_G=
OPTION_H=1 2
OPTION_I=foo\nbar
OPTION_J=\t`

	commentsEnv = `# Full line comment
qux=thud # fred # other
thud=fred#qux # other
fred=qux#baz # other # more
foo=bar # baz
bar=foo#baz
baz="foo"#bar
baz2="foo" ""#bar`

	quotedEnv = `OPTION_A='1'
OPTION_B='2'
OPTION_C=''
OPTION_D='\n'
OPTION_E="1"
OPTION_F="2"
OPTION_G=""
OPTION_H="\n"
OPTION_I = "echo 'asd'"
OPTION_J='line 1
line 2'
OPTION_K='line one
this is \'quoted\'
one more line'
OPTION_L="line 1
line 2"
OPTION_M="line one
this is \"quoted\"
one more line"`

	substitutionsEnv = `OPTION_A=1
OPTION_B=${OPTION_A}
OPTION_C=$OPTION_B
OPTION_D=${OPTION_A}${OPTION_B}
OPTION_E=
OPTION_F=${GLOBAL_OPTION}`

	exportedEnv = `export OPTION_A=2
export OPTION_B='\n'`

	equalsEnv = `export OPTION_A='postgres://localhost:5432/database?sslmode=disable'`

	invalid1Env = `INVALID LINE
foo=bar`
)

func TestFixtures(t *testing.T) {
	tests := []struct {
		name     string
		envFile  io.Reader
		subst    bool
		substEnv Env
		want     Env
		wantErr  bool
	}{
		{
			name:    "plain.env",
			envFile: strings.NewReader(plainEnv),
			want: Env{
				"OPTION_A": "1",
				"OPTION_B": "2",
				"OPTION_C": "3",
				"OPTION_D": "4",
				"OPTION_E": "5",
				"OPTION_H": "1 2",
				"OPTION_I": "foo\nbar",
				"OPTION_J": "\t",
			},
		},
		{
			name: "comments.env",

			envFile: strings.NewReader(commentsEnv),
			want: Env{
				"qux":  "thud",
				"thud": "fred#qux",
				"fred": "qux#baz",
				"foo":  "bar",
				"bar":  "foo#baz",
				"baz":  "foo",
				"baz2": `foo" "`,
			},
		},
		{
			name:    "quoted.env",
			envFile: strings.NewReader(quotedEnv),
			want: Env{
				"OPTION_A": "1",
				"OPTION_B": "2",
				"OPTION_C": "",
				"OPTION_D": `\n`,
				"OPTION_E": "1",
				"OPTION_F": "2",
				"OPTION_G": "",
				"OPTION_H": "\n",
				"OPTION_I": "echo 'asd'",
				"OPTION_J": "line 1\nline 2",
				"OPTION_K": "line one\nthis is \\'quoted\\'\none more line",
				"OPTION_L": "line 1\nline 2",
				"OPTION_M": "line one\nthis is \"quoted\"\none more line",
			},
		},
		{
			name:     "substitutions.env",
			envFile:  strings.NewReader(substitutionsEnv),
			subst:    true,
			substEnv: Env{"GLOBAL_OPTION": "global"},
			want: Env{
				"OPTION_A": "1",
				"OPTION_B": "1",
				"OPTION_C": "1",
				"OPTION_D": "11",
				"OPTION_E": "",
				"OPTION_F": "global",
			},
			wantErr: false,
		},
		{
			name:    "exported.env",
			envFile: strings.NewReader(exportedEnv),
			want: Env{
				"OPTION_A": "2",
				"OPTION_B": `\n`,
			},
		},
		{
			name:    "equals.env",
			envFile: strings.NewReader(equalsEnv),
			want: Env{
				"OPTION_A": "postgres://localhost:5432/database?sslmode=disable",
			},
		},
		{
			name:    "invalid1.env",
			envFile: strings.NewReader(invalid1Env),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.envFile, tt.subst, tt.substEnv)

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
