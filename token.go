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

func (t *Tokenizer) readByte() byte {
	b, err := t.r.ReadByte()
	if err != nil {
		t.done = true
		t.err = err
	}
	return b
}

func (t *Tokenizer) Scan() bool {
	if t.done {
		return false
	}
	t.token.Type = ErrorToken
	// var bytes []byte
	var b byte

	for b = t.readByte(); isspace(b); b = t.readByte() {
	}
	if t.err != nil {
		return false
	}

	switch {
	case isnum(b):
		var number uint
		for isnum(b) {
			t.token.Type = NumberToken
			number *= 10
			number += uint(b - '0')
			b = t.readByte()
		}
		t.token.Number = number
		if b == ':' {
			t.token.Type = StringToken
			t.token.Octets = make([]byte, number)
			_, err := io.ReadFull(t.r, t.token.Octets)
			if err != nil {
				t.token.Type = ErrorToken
				t.done = true
				t.err = err
				return false
			}
			b = t.readByte()
		}
	case b == '(':
		t.token.Type = LeftParenToken
		b = t.readByte()
	case b == ')':
		t.token.Type = RightParenToken
		b = t.readByte()
	case isalpha(b):
		t.token.Type = WordToken
		t.token.Word = ""
		for isalnum(b) {
			t.token.Word += string(b)
			b = t.readByte()
		}
	default:
		t.token.Type = ErrorToken
		t.err = fmt.Errorf("syntax error: unexpected \"%c\"", b)
		t.done = true
		return false
	}

	if !isspace(b) {
		t.err = fmt.Errorf("syntax error: expected space after %q", t.token)
		t.token.Type = ErrorToken
		t.done = true
		return false
	}
	return true
}

func (t *Tokenizer) Token() Token {
	return t.token
}

func (t *Tokenizer) Err() error {
	return t.err
}

func (t Token) String() string {
	s := ""
	switch t.Type {
	case ErrorToken:
		s = "**ERROR**"
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
