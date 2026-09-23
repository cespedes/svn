package svn

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// An ItemType is the type of an Item.
type ItemType int

const (
	// InvalidType is the zero value of ItemType: an Item that does not
	// represent any actual protocol element (e.g. the result of marshaling
	// a nil pointer).
	InvalidType ItemType = iota
	// WordType is a bare word, such as a command name, a boolean
	// ("true"/"false") or an enum value: letters, digits and '-' only.
	WordType
	// NumberType is an unsigned integer.
	NumberType
	// StringType is a length-prefixed byte string ("N:the actual bytes"),
	// used for arbitrary binary or text content that a word cannot hold.
	StringType
	// ListType is a parenthesized, whitespace-separated sequence of items.
	ListType
)

// Item represents a syntactic element in the SVN protocol.
type Item struct {
	// Type specifies the type of item, and which of the next fields is used
	// to represent it.
	Type ItemType
	// Number holds the value when Type is NumberType; unused otherwise.
	Number uint
	// Text holds the content when Type is WordType or StringType; unused
	// otherwise.
	Text string
	// List holds the elements when Type is ListType; unused otherwise.
	List []Item
}

// String returns a string representation of the Item.
func (i Item) String() string {
	s := ""
	switch i.Type {
	case WordType:
		s = i.Text
	case NumberType:
		s = fmt.Sprint(i.Number)
	case StringType:
		s = fmt.Sprintf("%d:%s", len(i.Text), i.Text)
	case ListType:
		s = "( "
		for _, elem := range i.List {
			s += elem.String()
			s += " "
		}
		s += ")"
	}
	return s
}

// An Itemizer returns a stream of SVN Items.
type Itemizer struct {
	r *bufio.Reader
}

// NewItemizer returns a new SVN Itemizer for the given Reader.
func NewItemizer(r io.Reader) *Itemizer {
	z := &Itemizer{
		r: bufio.NewReader(r),
	}
	return z
}

var errRightParen = errors.New("right parentesis")

// Item returns the next Item from the Itemizer. It returns io.EOF once the
// underlying Reader is exhausted, or a syntax error if the input does not
// follow the protocol's grammar.
func (i *Itemizer) Item() (Item, error) {
	var item Item
	t := NewTokenizer(i.r)

	t.Scan()
	if err := t.Err(); err != nil {
		return Item{}, err
	}
	tt := t.Token()
	switch tt.Type {
	case WordToken:
		item.Type = WordType
		item.Text = tt.Text
	case NumberToken:
		item.Type = NumberType
		item.Number = tt.Number
	case StringToken:
		item.Type = StringType
		item.Text = tt.Text
	case LeftParenToken:
		item.Type = ListType
		for {
			newItem, err := i.Item()
			if err != nil && err != errRightParen {
				return Item{}, err
			}
			if err == errRightParen {
				break
			}
			item.List = append(item.List, newItem)
		}
	case RightParenToken:
		return Item{}, errRightParen
	}
	return item, nil
}
