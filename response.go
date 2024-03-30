package svn

import (
	"fmt"
)

// ParseResponse expects an item following the prototype of "command response"
// and returns the list of params if the type is "success".
// It returns error otherwise.
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
