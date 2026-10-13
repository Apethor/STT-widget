//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"

	"ai-talker/internal/desktop"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "informe uma pasta isolada para o smoke nativo")
		os.Exit(2)
	}
	if err := desktop.RunWindowSmoke(context.Background(), os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
