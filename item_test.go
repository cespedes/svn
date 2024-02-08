package svn

import (
	"io"
	"strings"
	"testing"
)

type itemTest struct {
	// A short description of the test case.
	desc string
	// The input to parse.
	input string
	// The string representations of the expected item
	golden string
}

var itemTests = []tokenTest{
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
	// String
	{
		"string",
		"8:elephant ",
		"8:elephant",
	},
	// svnserve
	{
		"svnserve",
		"( \t\r success ( 2 2 ( ) ( edit-pipeline svndiff1 accepts-svndiff2 absent-entries commit-revprops depth log-revprops atomic-revprops partial-replay inherited-props ephemeral-txnprops file-revs-reverse list ) ) )  ",
		"( success ( 2 2 ( ) ( edit-pipeline svndiff1 accepts-svndiff2 absent-entries commit-revprops depth log-revprops atomic-revprops partial-replay inherited-props ephemeral-txnprops file-revs-reverse list ) ) )",
	},
	// example with different types of spaces
	{
		"different spaces",
		"(   word \t 22\n6:string ( sublist ) \r \v)\f",
		"( word 22 6:string ( sublist ) )",
	},
}

type itemErrorTest struct {
	// A short description of the test case.
	desc string
	// The input to parse.
	input string
}

var itemErrorTests = []tokenErrorTest{
	{
		"empty",
		"",
	},
	{
		"spaces",
		" \t \r \n ",
	},
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
	{
		"only right paren",
		") ",
	},
	{
		"unbalanced parens",
		"( foo ( bar ) ",
	},
}

func TestItemizer(t *testing.T) {
	for _, tt := range itemTests {
		t.Run(tt.desc, func(t *testing.T) {
			z := NewItemizer(strings.NewReader(tt.input))
			item, err := z.Item()
			if err != nil {
				t.Errorf("%s: want %q got error %v", tt.desc, tt.golden, err)
				return
			}
			actual := item.String()
			if tt.golden != actual {
				t.Errorf("%s: want %q got %q", tt.desc, tt.golden, actual)
				return
			}
			item, err = z.Item()
			switch err {
			case nil:
				t.Errorf("%s: want EOF got %q", tt.desc, item.String())
			case io.EOF:
			default:
				t.Errorf("%s: want EOF got error %v", tt.desc, err)
			}
		})
	}
	for _, tt := range itemErrorTests {
		t.Run(tt.desc, func(t *testing.T) {
			z := NewItemizer(strings.NewReader(tt.input))
			item, err := z.Item()
			if err == nil {
				t.Errorf("%s: expected error got %q", tt.desc, item.String())
				return
			}
		})
	}
}
