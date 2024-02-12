package main

import (
	"fmt"
	"log"

	"github.com/cespedes/svn"
)

func main() {
	var values = []any{
		23,
		3.14,
		"foo",
	}
	for _, v := range values {
		i, err := svn.Marshal(v)
		if err != nil {
			log.Fatalf("Marshaling %v: %v", v, err)
		}
		fmt.Printf("Marshaling %v: %v\n", v, i)
	}
}
