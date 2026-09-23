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
