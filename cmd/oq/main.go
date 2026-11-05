package main

import (
	"os"

	"github.com/Edwinexd/openapi-query/internal/oq"
)

func main() {
	os.Exit(oq.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
