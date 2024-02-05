package svn

import (
	"io"
	"strings"
	"testing"
)

type tokenTest struct {
	// A short description of the test case.
	desc string
	// The input to parse.
	input string
	// The string representations of the expected tokens, joined by '$'.
	golden string
}

var tokenTests = []tokenTest{
	{
		"empty",
		"",
		"",
	},
	// A number
	{
		"number",
		"42 ",
		"42",
	},
}

func TestTokenizer(t *testing.T) {
	for _, tt := range tokenTests {
		t.Run(tt.desc, func(t *testing.T) {
			z := NewTokenizer(strings.NewReader(tt.input))
			if tt.golden != "" {
				for i, s := range strings.Split(tt.golden, "$") {
					if z.Scan() == false {
						t.Errorf("%s token %d: want %q got error %v", tt.desc, i, s, z.Err())
						return
					}
					actual := z.Token().String()
					if s != actual {
						t.Errorf("%s token %d: want %q got %q", tt.desc, i, s, actual)
						return
					}
				}
			}
			z.Scan()
			if z.Err() != io.EOF {
				t.Errorf("%s: want EOF got %q", tt.desc, z.Err())
			}
		})
	}
}
