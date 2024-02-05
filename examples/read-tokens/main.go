package main

import (
	"fmt"
	"os"

	"github.com/cespedes/svn"
)

func main() {
	t := svn.NewTokenizer(os.Stdin)

	for t.Scan() {
		fmt.Printf("Token: %s\n", t.Token())
	}
}
