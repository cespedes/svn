package main

import (
	"fmt"

	"github.com/cespedes/svn"
)

type T struct {
	A int
	B int
	C []int
	D *T
}

func main() {
	a := 3
	pa := &a
	var t T
	t.A = 5
	t.B = 7
	t.C = []int{9, 10, 11}
	t.D = &T{
		A: 30,
		B: 40,
	}
	var values = []any{
		23,
		3.14,
		"foo",
		a,
		&a,
		&pa,
		[3]int{1, 2, 3},
		[2]uint8{5, 27},
		[2]string{"foo", "bar"},
		[]string{"foo", "bar"},
		struct {
			A int
			B string
		}{A: 42, B: "forty-two"},
		T{A: 1, B: 2},
		t,
		nil,
		false,
		[]byte("hola caracola"),
	}
	for _, v := range values {
		i, err := svn.Marshal(v)
		if err != nil {
			fmt.Printf("Error marshaling %v: %v\n", v, err)
			continue
		}
		fmt.Printf("Marshaling %v: %v\n", v, i)
	}
}
