package svn

import (
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
		return nil, fmt.Errorf("syntax error: response type is not list")
	}
	if len(i.List) != 2 {
		return nil, fmt.Errorf("syntax error: response with %d fields", len(i.List))
	}
	if i.List[0].Type != WordType {
		return nil, fmt.Errorf("syntax error: first item in response is not a word")
	}
	if i.List[1].Type != ListType {
		return nil, fmt.Errorf("syntax error: second item in response is not a list")
	}
	if i.List[0].Word == "success" {
		return i.List[1].List, nil
	}
	if i.List[0].Word != "failure" {
		return nil, fmt.Errorf("syntax error: response must be `success` or `failure`")
	}
	if len(i.List[1].List) != 1 {
		return nil, fmt.Errorf("syntax error: error response must have a 1-element list")
	}
	if i.List[1].List[0].Type != ListType {
		return nil, fmt.Errorf("syntax error: error response must be a list in a list")
	}
	if len(i.List[1].List[0].List) != 4 {
		return nil, fmt.Errorf("syntax error: error response must have 4 params, found %d", len(i.List[1].List[0].List))
	}
	if i.List[1].List[0].List[0].Type != NumberType ||
		i.List[1].List[0].List[1].Type != StringType ||
		i.List[2].List[0].List[1].Type != StringType ||
		i.List[3].List[0].List[1].Type != NumberType {
		return nil, fmt.Errorf("syntax error: error response params have incorrect types")
	}
	return nil, Error{
		AprErr:  int(i.List[1].List[0].List[1].Number),
		Message: string(i.List[1].List[0].List[1].Octets),
		File:    string(i.List[1].List[0].List[2].Octets),
		Line:    int(i.List[1].List[0].List[3].Number),
	}
}

// ParseError parses a "failure" command response and returns an Error
// if it can be parsed.
func ParseError(response error) (Error, error) {
	if e, ok := response.(Error); ok {
		return e, nil
	}
	return Error{}, response
}
