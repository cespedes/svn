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
	// The string representations of the expected tokens, joined by '^'.
	golden string
}

var tokenTests = []tokenTest{
	{
		"empty",
		"",
		"",
	},
	{
		"spaces",
		" \t \r \n ",
		"",
	},
	// A number
	{
		"number",
		"42 ",
		"42",
	},
	// A word
	{
		"word",
		"sesame ",
		"sesame",
	},
	// Alphanumeric word
	{
		"alphanumeric word",
		"plan42 ",
		"plan42",
	},
	// String
	{
		"string",
		"8:elephant ",
		"8:elephant",
	},
	// svnserve
	{
		"svnserve",
		"( success ( 2 2 ( ) ( edit-pipeline svndiff1 accepts-svndiff2 absent-entries commit-revprops depth log-revprops atomic-revprops partial-replay inherited-props ephemeral-txnprops file-revs-reverse list ) ) )  ",
		"(^success^(^2^2^(^)^(^edit-pipeline^svndiff1^accepts-svndiff2^absent-entries^commit-revprops^depth^log-revprops^atomic-revprops^partial-replay^inherited-props^ephemeral-txnprops^file-revs-reverse^list^)^)^)",
	},
	// example with different types of spaces
	{
		"different spaces",
		"(   word \t 22\n6:string ( sublist ) \r \v)\f",
		"(^word^22^6:string^(^sublist^)^)",
	},
}

type tokenErrorTest struct {
	// A short description of the test case.
	desc string
	// The input to parse.
	input string
}

var tokenErrorTests = []tokenErrorTest{
	{
		"no space after",
		"42",
	},
	{
		"negative number",
		"-42 ",
	},
	{
		"number and letters",
		"42foo ",
	},
	{
		"letters and symbols",
		"foo/ ",
	},
	{
		"short string",
		"4:foo",
	},
	{
		"long string",
		"2:foo ",
	},
}

func TestTokenizer(t *testing.T) {
	for _, tt := range tokenTests {
		t.Run(tt.desc, func(t *testing.T) {
			z := NewTokenizer(strings.NewReader(tt.input))
			if tt.golden != "" {
				for i, s := range strings.Split(tt.golden, "^") {
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
			switch z.Err() {
			case nil:
				t.Errorf("%s: want EOF got %q", tt.desc, z.Token().String())
			case io.EOF:
			default:
				t.Errorf("%s: want EOF got error %v", tt.desc, z.Err())
			}
		})
	}
	for _, tt := range tokenErrorTests {
		t.Run(tt.desc, func(t *testing.T) {
			z := NewTokenizer(strings.NewReader(tt.input))
			for z.Scan() {
			}
			if z.Err() == io.EOF {
				t.Errorf("%s: expected error got %q", tt.desc, z.Token().String())
				return
			}
		})
	}
}
