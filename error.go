package svn

import "fmt"

type Error struct {
	AprErr  int
	Message string `svn:",xxx"`
	File    string `svn:",xxx"`
	Line    int
}

func (e Error) Error() string {
	msg := ""
	if e.AprErr != 0 {
		msg = fmt.Sprintf("%d ", e.AprErr)
	}
	msg += e.Message
	if e.File != "" {
		msg += fmt.Sprintf(" (%s line %d)", e.File, e.Line)
	}
	return msg
}
