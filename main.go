package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	fmt.Fprintf(os.Stderr, "c2 %s — not yet implemented\n", version)
	os.Exit(1)
}
