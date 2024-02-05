package svn

import (
	"bufio"
	"fmt"
	"io"
)

type TokenType int

const (
	// ErrorToken means that an error occurred during tokenization.
	ErrorToken TokenType = iota
	WordToken
	NumberToken
	StringToken
	LeftParenToken
	RightParenToken
)

type Tokenizer struct {
	r     *bufio.Reader
	token Token
	err   error
	done  bool
}

type Token struct {
	Type   TokenType
	Word   string
	Number uint
	Octets []byte
}

// NewTokenizerFragment returns a new SVN Tokenizer for the given Reader.
func NewTokenizer(r io.Reader) *Tokenizer {
	z := &Tokenizer{
		r: bufio.NewReader(r),
	}
	return z
}

func (t *Tokenizer) Scan() bool {
	if t.done {
		return false
	}
	b, err := t.r.ReadByte()
	if err != nil {
		t.done = true
		t.err = io.EOF
		return false
	}
	t.token = Token{
		Type: WordToken,
		Word: fmt.Sprintf("%c", b),
	}
	return true
}

func (t *Tokenizer) Token() Token {
	return t.token
}

func (t Token) String() string {
	s := ""
	switch t.Type {
	case WordToken:
		s = t.Word
	case NumberToken:
		s = fmt.Sprint(t.Number)
	case StringToken:
		s = fmt.Sprintf("%d:%s", len(t.Octets), t.Octets)
	case LeftParenToken:
		s = "("
	case RightParenToken:
		s = ")"
	}
	return s
}
