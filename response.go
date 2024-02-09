package svn

import (
	"errors"
	"fmt"
)

type ResponseType int

const (
	ResponseSuccess ItemType = iota
	ResponseFailure
)

type Error struct {
	AprErr  int
	Message string
	File    string
	Line    int
}

func (e Error) Error() string {
	ret := fmt.Sprintf("%d %s", e.AprErr, e.Message)
	if e.File != "" {
		ret += fmt.Sprintf(" (%s line %d)", e.File, e.Line)
	}
	return ret
}

// ParseResponse expects an item following the prototype of "command response"
// and returns the list of params if the type is "success".
// It returns error otherwise.
func ParseResponse(i Item) ([]Item, error) {
	if i.Type != ListType {
		return nil, errors.New("syntax error")
	}
	return nil, nil
}

// ParseError parses a "failure" command response and returns an Error
// if it can be parsed.
func ParseError(response error) (Error, error) {
	if e, ok := response.(Error); ok {
		return e, nil
	}
	return Error{}, response
}
