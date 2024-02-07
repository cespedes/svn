package main

import (
	"fmt"
	"os"

	"github.com/cespedes/svn"
)

func main() {
	i := svn.NewItemizer(os.Stdin)

	for {
		i, err := i.Item()

		if err != nil {
			fmt.Printf("Error: %s\n", err)
			break
		}

		fmt.Printf("Item: %s\n", i)
	}

}
