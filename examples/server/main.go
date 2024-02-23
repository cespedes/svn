package main

import (
	"errors"
	"log"
	"os"

	"github.com/cespedes/svn"
)

func login(greet svn.Greet) error {
	return nil
}

func callback(cmd svn.Command, conn svn.Conn) error {
	//log.Printf("command: %v\n", command)
	switch cmd.Name {
	case "get-latest-rev":
		// empty auth-request:
		conn.WriteSuccess([]any{[]any{}, []byte{}})

		// fake latest revision:
		conn.WriteSuccess([]any{1000})
	case "stat":
		// empty auth-request:
		conn.WriteSuccess([]any{[]any{}, []byte{}})

		// sending fake stat response:
		conn.WriteSuccess([]any{
			[]any{
				[]any{
					"dir",
					uint64(18446744073709551615),
					"false",
					1000,
					[]any{
						[]byte("2024-02-23T14:56:05.241020Z"),
					},
					[]any{[]byte("cespedes")},
				},
			},
		})
	case "get-lock":
		// empty auth-request:
		conn.WriteSuccess([]any{[]any{}, []byte{}})

		// sending no locks:
		conn.WriteSuccess([]any{[]any{}})
	default:
		conn.WriteFailure(errors.New("unimplemented"))
	}
	return nil
}

func main() {
	err := svn.Serve(os.Stdin, os.Stdout, login, callback)
	if err != nil {
		log.Fatal(err)
	}
}
