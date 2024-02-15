package svn

import (
	"fmt"
)

type ResponseType int

const (
	ResponseSuccess ItemType = iota
	ResponseFailure
)

type Response struct {
	Type   string
	Params Item
}

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
func ParseResponse(i Item) (Item, error) {
	var resp Response
	err := Unmarshal(i, &resp)
	if err != nil {
		return Item{}, err
	}
	if resp.Params.Type != ListType {
		return Item{}, fmt.Errorf("syntax error: response type must be a list")
	}
	switch resp.Type {
	case "success":
		return resp.Params, nil
	case "failure":
		var errResp struct {
			Err Error
		}
		err = Unmarshal(resp.Params, &errResp)
		if err != nil {
			return Item{}, err
		}
		return Item{}, errResp.Err
	default:
		return Item{}, fmt.Errorf("syntax error: response must be `success` or `failure`")
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
