package main

import (
	"fmt"
	"os"

	"github.com/alecthomas/repr"
)

func printRepr(value any) {
	valueRepr := repr.String(value, repr.Indent("\t"), repr.OmitEmpty(false))
	fmt.Fprintf(os.Stderr, "%s\n\n", valueRepr)
}
