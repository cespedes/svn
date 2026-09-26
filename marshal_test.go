package svn

import (
	"reflect"
	"testing"
)

type marshalTest struct {
	// A short description of the test case.
	desc string
	// The value to marshal.
	input any
	// The string representation of the expected Item.
	golden string
}

type withUnexported struct {
	A      string
	B      int
	hidden string
}

type nested struct {
	Name  string
	Inner struct {
		X int
		Y int
	}
}

var marshalTests = []marshalTest{
	{"bool true", true, "true"},
	{"bool false", false, "false"},
	{"int", 42, "42"},
	{"uint", uint(42), "42"},
	{"string", "hello", "hello"},
	{"bytes", []byte("hello"), "5:hello"},
	{"string slice", []string{"a", "bb"}, "( a bb )"},
	{"empty slice", []string{}, "( )"},
	{"nil slice", []string(nil), "( )"},
	{"nil pointer", (*int)(nil), ""},
	{"non-nil pointer", ptr(42), "42"},
	{"struct", struct {
		A string
		B int
	}{"x", 1}, "( x 1 )"},
	{"struct skips unexported fields", withUnexported{"x", 1, "hidden"}, "( x 1 )"},
	{"item passthrough", Item{Type: WordType, Text: "raw"}, "raw"},
}

func ptr[T any](v T) *T {
	return &v
}

func TestMarshal(t *testing.T) {
	for _, tt := range marshalTests {
		t.Run(tt.desc, func(t *testing.T) {
			item, err := Marshal(tt.input)
			if err != nil {
				t.Fatalf("%s: Marshal(%#v) returned error %v", tt.desc, tt.input, err)
			}
			if actual := item.String(); actual != tt.golden {
				t.Errorf("%s: Marshal(%#v) = %q, want %q", tt.desc, tt.input, actual, tt.golden)
			}
		})
	}

	t.Run("nested struct", func(t *testing.T) {
		var n nested
		n.Name = "foo"
		n.Inner.X = 1
		n.Inner.Y = 2
		item, err := Marshal(n)
		if err != nil {
			t.Fatalf("Marshal returned error %v", err)
		}
		want := "( foo ( 1 2 ) )"
		if actual := item.String(); actual != want {
			t.Errorf("Marshal(%#v) = %q, want %q", n, actual, want)
		}
	})

	t.Run("struct with nil pointer field is dropped from the list", func(t *testing.T) {
		s := struct {
			A string
			B *int
			C string
		}{"x", nil, "z"}
		item, err := Marshal(s)
		if err != nil {
			t.Fatalf("Marshal returned error %v", err)
		}
		// Note: a nil pointer field marshals to nothing, so it disappears
		// from the resulting list rather than becoming an empty element.
		// This means the number of elements in the encoded list may not
		// match the number of fields in the struct.
		want := "( x z )"
		if actual := item.String(); actual != want {
			t.Errorf("Marshal(%#v) = %q, want %q", s, actual, want)
		}
	})
}

func TestUnmarshal(t *testing.T) {
	t.Run("word true into bool", func(t *testing.T) {
		var b bool
		if err := Unmarshal(Item{Type: WordType, Text: "true"}, &b); err != nil {
			t.Fatal(err)
		}
		if !b {
			t.Errorf("got false, want true")
		}
	})

	t.Run("word false into bool", func(t *testing.T) {
		b := true
		if err := Unmarshal(Item{Type: WordType, Text: "false"}, &b); err != nil {
			t.Fatal(err)
		}
		if b {
			t.Errorf("got true, want false")
		}
	})

	t.Run("word into string", func(t *testing.T) {
		var s string
		if err := Unmarshal(Item{Type: WordType, Text: "hello"}, &s); err != nil {
			t.Fatal(err)
		}
		if s != "hello" {
			t.Errorf("got %q, want %q", s, "hello")
		}
	})

	t.Run("word into nil string pointer allocates it", func(t *testing.T) {
		var s *string
		if err := Unmarshal(Item{Type: WordType, Text: "hello"}, &s); err != nil {
			t.Fatal(err)
		}
		if s == nil || *s != "hello" {
			t.Errorf("got %v, want pointer to %q", s, "hello")
		}
	})

	t.Run("string into []byte", func(t *testing.T) {
		var b []byte
		if err := Unmarshal(Item{Type: StringType, Text: "hello"}, &b); err != nil {
			t.Fatal(err)
		}
		if string(b) != "hello" {
			t.Errorf("got %q, want %q", b, "hello")
		}
	})

	t.Run("string into string", func(t *testing.T) {
		var s string
		if err := Unmarshal(Item{Type: StringType, Text: "hello"}, &s); err != nil {
			t.Fatal(err)
		}
		if s != "hello" {
			t.Errorf("got %q, want %q", s, "hello")
		}
	})

	t.Run("number into int", func(t *testing.T) {
		var n int
		if err := Unmarshal(Item{Type: NumberType, Number: 42}, &n); err != nil {
			t.Fatal(err)
		}
		if n != 42 {
			t.Errorf("got %d, want 42", n)
		}
	})

	t.Run("number into uint", func(t *testing.T) {
		var n uint
		if err := Unmarshal(Item{Type: NumberType, Number: 42}, &n); err != nil {
			t.Fatal(err)
		}
		if n != 42 {
			t.Errorf("got %d, want 42", n)
		}
	})

	t.Run("list into struct, positionally", func(t *testing.T) {
		var s struct {
			A string
			B int
		}
		item := Item{Type: ListType, List: []Item{
			{Type: WordType, Text: "x"},
			{Type: NumberType, Number: 1},
		}}
		if err := Unmarshal(item, &s); err != nil {
			t.Fatal(err)
		}
		want := struct {
			A string
			B int
		}{"x", 1}
		if s != want {
			t.Errorf("got %+v, want %+v", s, want)
		}
	})

	t.Run("list with fewer elements than struct fields leaves the rest zero", func(t *testing.T) {
		var s struct {
			A string
			B int
		}
		item := Item{Type: ListType, List: []Item{
			{Type: WordType, Text: "x"},
		}}
		if err := Unmarshal(item, &s); err != nil {
			t.Fatal(err)
		}
		want := struct {
			A string
			B int
		}{"x", 0}
		if s != want {
			t.Errorf("got %+v, want %+v", s, want)
		}
	})

	t.Run("list into slice", func(t *testing.T) {
		var s []string
		item := Item{Type: ListType, List: []Item{
			{Type: WordType, Text: "a"},
			{Type: WordType, Text: "b"},
		}}
		if err := Unmarshal(item, &s); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(s, []string{"a", "b"}) {
			t.Errorf("got %v, want %v", s, []string{"a", "b"})
		}
	})

	t.Run("empty list into a pointer leaves it nil (optional value)", func(t *testing.T) {
		var rev *int
		item := Item{Type: ListType}
		if err := Unmarshal(item, &rev); err != nil {
			t.Fatal(err)
		}
		if rev != nil {
			t.Errorf("got %v, want nil", *rev)
		}
	})

	t.Run("single-element list into a pointer sets it", func(t *testing.T) {
		var rev *int
		item := Item{Type: ListType, List: []Item{
			{Type: NumberType, Number: 7},
		}}
		if err := Unmarshal(item, &rev); err != nil {
			t.Fatal(err)
		}
		if rev == nil || *rev != 7 {
			t.Errorf("got %v, want pointer to 7", rev)
		}
	})

	t.Run("nested single-element list collapses onto a scalar", func(t *testing.T) {
		var n int
		item := Item{Type: ListType, List: []Item{
			{Type: ListType, List: []Item{
				{Type: NumberType, Number: 5},
			}},
		}}
		if err := Unmarshal(item, &n); err != nil {
			t.Fatal(err)
		}
		if n != 5 {
			t.Errorf("got %d, want 5", n)
		}
	})

	t.Run("nested struct", func(t *testing.T) {
		var n nested
		item := Item{Type: ListType, List: []Item{
			{Type: WordType, Text: "foo"},
			{Type: ListType, List: []Item{
				{Type: NumberType, Number: 1},
				{Type: NumberType, Number: 2},
			}},
		}}
		if err := Unmarshal(item, &n); err != nil {
			t.Fatal(err)
		}
		if n.Name != "foo" || n.Inner.X != 1 || n.Inner.Y != 2 {
			t.Errorf("got %+v", n)
		}
	})

	t.Run("into unexported field fails", func(t *testing.T) {
		var s withUnexported
		item := Item{Type: ListType, List: []Item{
			{Type: WordType, Text: "x"},
			{Type: NumberType, Number: 1},
			{Type: WordType, Text: "hidden"},
		}}
		if err := Unmarshal(item, &s); err == nil {
			t.Errorf("expected error, got none")
		}
	})

	t.Run("non-pointer destination fails", func(t *testing.T) {
		var s string
		if err := Unmarshal(Item{Type: WordType, Text: "x"}, s); err == nil {
			t.Errorf("expected error, got none")
		}
	})

	t.Run("nil pointer destination fails", func(t *testing.T) {
		var s *string
		if err := Unmarshal(Item{Type: WordType, Text: "x"}, s); err == nil {
			t.Errorf("expected error, got none")
		}
	})
}

// TestMarshalUnmarshalStructuralRoundTrip checks that Marshal and Unmarshal
// are inverses of each other at the Item level: marshaling a value and then
// unmarshaling the resulting Item back yields an equal value.
//
// This intentionally stays at the Item level, without going through
// Item.String()/Itemizer: Marshal always encodes a Go string as a WordType,
// and the wire "word" syntax only allows alphanumerics and '-', so a string
// containing e.g. '/' or ':' cannot survive being printed and re-tokenized.
// That is not a bug: in this codebase, values with arbitrary text (paths,
// dates, URLs, ...) are always sent over the wire as []byte (StringType),
// built by hand in client.go/server.go; a bare Go `string` field is only
// ever used for values that are already known to be safe "words" (booleans,
// enums like a Dirent's Kind, command names), or as an Unmarshal target for
// a StringType coming from the network. See TestUnmarshalWireStrings below
// for that path.
func TestMarshalUnmarshalStructuralRoundTrip(t *testing.T) {
	type inner struct {
		X int
		Y int
	}
	type value struct {
		Name    string
		Flag    bool
		Content []byte
		Tags    []string
		Inner   inner
	}

	want := value{
		Name:    "word-safe-name",
		Flag:    true,
		Content: []byte("arbitrary: text/with punctuation"),
		Tags:    []string{"a", "b"},
		Inner:   inner{X: 1, Y: 2},
	}

	item, err := Marshal(want)
	if err != nil {
		t.Fatalf("Marshal(%#v) returned error %v", want, err)
	}

	var got value
	if err := Unmarshal(item, &got); err != nil {
		t.Fatalf("Unmarshal(%s): %v", item, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}
}

// TestUnmarshalWireStrings checks that Unmarshal correctly populates
// response-style structs (as declared in types.go) from Items shaped the
// way a real SVN server sends them: free-form text as StringType, exactly
// as the Itemizer would produce it while parsing a real response.
func TestUnmarshalWireStrings(t *testing.T) {
	item := Item{Type: ListType, List: []Item{
		{Type: StringType, Text: "trunk/foo.txt"},
		{Type: WordType, Text: "file"},
		{Type: NumberType, Number: 123},
		{Type: WordType, Text: "true"},
		{Type: NumberType, Number: 42},
		{Type: ListType, List: []Item{{Type: StringType, Text: "2024-04-02T13:37:34.350221Z"}}},
		{Type: ListType, List: []Item{{Type: StringType, Text: "juan"}}},
	}}

	var got Dirent
	if err := Unmarshal(item, &got); err != nil {
		t.Fatalf("Unmarshal(%s): %v", item, err)
	}
	want := Dirent{
		Path:        "trunk/foo.txt",
		Kind:        "file",
		Size:        123,
		HasProps:    true,
		CreatedRev:  42,
		CreatedDate: "2024-04-02T13:37:34.350221Z",
		LastAuthor:  "juan",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestUnmarshalLogEntryChanged replays a real svnserve's "log" response for
// a revision that both modifies a file and adds one as a copy of another,
// captured by hand-driving the wire protocol against a real repository.
// This is the exact shape a previous bug got wrong: Path (and CopyPath)
// must decode correctly from a length-prefixed string, and Copy/Info must
// each come from their own independently-optional nested list.
func TestUnmarshalLogEntryChanged(t *testing.T) {
	item := Item{Type: ListType, List: []Item{
		{Type: ListType, List: []Item{ // Changed
			{Type: ListType, List: []Item{ // "/trunk/README.md": modified, no copy
				{Type: StringType, Text: "/trunk/README.md"},
				{Type: WordType, Text: "M"},
				{Type: ListType}, // no copy-from info
				{Type: ListType, List: []Item{
					{Type: StringType, Text: "file"},
					{Type: WordType, Text: "true"},
					{Type: WordType, Text: "false"},
				}},
			}},
			{Type: ListType, List: []Item{ // "/trunk/main_copy.go": added as a copy
				{Type: StringType, Text: "/trunk/main_copy.go"},
				{Type: WordType, Text: "A"},
				{Type: ListType, List: []Item{
					{Type: StringType, Text: "/trunk/main.go"},
					{Type: NumberType, Number: 1},
				}},
				{Type: ListType, List: []Item{
					{Type: StringType, Text: "file"},
					{Type: WordType, Text: "false"},
					{Type: WordType, Text: "false"},
				}},
			}},
		}},
		{Type: NumberType, Number: 2}, // Rev
		{Type: ListType, List: []Item{{Type: StringType, Text: "cespedes"}}},
		{Type: ListType, List: []Item{{Type: StringType, Text: "2026-09-26T15:59:22.454971Z"}}},
		{Type: ListType, List: []Item{{Type: StringType, Text: "modify README, copy main.go"}}},
		{Type: WordType, Text: "false"}, // has-children
		{Type: WordType, Text: "false"}, // invalid-revnum
		{Type: NumberType, Number: 0},   // revprop-count
		{Type: ListType},                // rev-props
		{Type: WordType, Text: "false"}, // subtractive-merge
	}}

	var entry LogEntry
	if err := Unmarshal(item, &entry); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if entry.Rev != 2 {
		t.Errorf("Rev = %d, want 2", entry.Rev)
	}
	if entry.Author != "cespedes" {
		t.Errorf("Author = %q, want %q", entry.Author, "cespedes")
	}
	if len(entry.Changed) != 2 {
		t.Fatalf("len(Changed) = %d, want 2 (Changed: %+v)", len(entry.Changed), entry.Changed)
	}

	readme := entry.Changed[0]
	if readme.Path != "/trunk/README.md" || readme.Mode != "M" {
		t.Errorf("Changed[0] = %+v, want Path=/trunk/README.md Mode=M", readme)
	}
	if readme.Copy != nil {
		t.Errorf("Changed[0].Copy = %+v, want nil", readme.Copy)
	}
	if readme.Info == nil || readme.Info.NodeKind != "file" || !readme.Info.TextMods || readme.Info.PropMods {
		t.Errorf("Changed[0].Info = %+v, want &{file true false}", readme.Info)
	}

	mainCopy := entry.Changed[1]
	if mainCopy.Path != "/trunk/main_copy.go" || mainCopy.Mode != "A" {
		t.Errorf("Changed[1] = %+v, want Path=/trunk/main_copy.go Mode=A", mainCopy)
	}
	if mainCopy.Copy == nil || mainCopy.Copy.Path != "/trunk/main.go" || mainCopy.Copy.Rev != 1 {
		t.Errorf("Changed[1].Copy = %+v, want &{/trunk/main.go 1}", mainCopy.Copy)
	}
	if mainCopy.Info == nil || mainCopy.Info.NodeKind != "file" || mainCopy.Info.TextMods || mainCopy.Info.PropMods {
		t.Errorf("Changed[1].Info = %+v, want &{file false false}", mainCopy.Info)
	}
}
