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
	InvalidType ItemType = iota
	WordType
	NumberType
	StringType
	ListType
)

// Item represents a syntactic element in the SVN protocol.
type Item struct {
	// Type specifies the type of item, and which of the next fields is used
	// to represent it.
	Type   ItemType
	Word   string
	Number uint
	Octets []byte
	List   []Item
}

// String returns a string representation of the Item.
func (i Item) String() string {
	s := ""
	switch i.Type {
	case WordType:
		s = i.Word
	case NumberType:
		s = fmt.Sprint(i.Number)
	case StringType:
		s = fmt.Sprintf("%d:%s", len(i.Octets), i.Octets)
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

// Item returns the next Item from the Itemizer
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
		item.Word = tt.Word
	case NumberToken:
		item.Type = NumberType
		item.Number = tt.Number
	case StringToken:
		item.Type = StringType
		item.Octets = tt.Octets
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
