package svn

import (
	"fmt"
)

// ParseResponse expects i to be shaped like a "command response"
// ( success params:list ) or ( failure ( err:error ... ) ), and returns
// the params list when the response reports success. On failure, it
// returns the reported [Error] as the error value; for any other shape,
// it returns a generic syntax error.
func ParseResponse(i Item) (Item, error) {
	var resp struct {
		Type   string
		Params Item
	}
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
		// failure params are "( err:error ... )": a list of one or more
		// error tuples. This package's Error only models a single error,
		// so take the first one. (A previous version of this code
		// unmarshaled straight into a one-field "struct{ Err Error }"
		// wrapper, which -- because of how Unmarshal special-cases a
		// struct with a single struct-typed field -- only ever filled
		// Err.AprErr, leaving Message/File/Line silently empty.)
		var errs []Error
		if err = Unmarshal(resp.Params, &errs); err != nil {
			return Item{}, err
		}
		if len(errs) == 0 {
			return Item{}, fmt.Errorf("syntax error: failure response with no error object")
		}
		return Item{}, errs[0]
	default:
		return Item{}, fmt.Errorf("syntax error: response must be `success` or `failure`")
	}
}
