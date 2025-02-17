package starlarkutil

import (
	"go.starlark.net/starlark"

	"dbohdan.com/regular/shellquote"
)

func AddPredeclared(d starlark.StringDict) {
	d["quote"] = starlark.NewBuiltin("quote", Quote)
}

func Quote(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	var shell string = "posix"

	if err := starlark.UnpackPositionalArgs(b.Name(), args, kwargs, 1, &s, &shell); err != nil {
		return starlark.String(""), err
	}

	quoted, err := shellquote.Quote(s, shell)
	if err != nil {
		return starlark.String(""), err
	}

	return starlark.String(quoted), nil
}
