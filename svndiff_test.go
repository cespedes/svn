package svn

import (
	"bytes"
	"fmt"
	"testing"
)

// TestDecodeSvndiffSpecExample replays, byte for byte, the worked example
// from Subversion's own svndiff format spec ("notes/svndiff": source
// "aaaabbbbcccc", target "aaaaccccdddddddd"), to check decodeSvndiff
// against an independent ground truth before trusting it to verify
// EncodeSvndiff.
func TestDecodeSvndiffSpecExample(t *testing.T) {
	source := []byte("aaaabbbbcccc")
	doc := []byte{
		'S', 'V', 'N', 0, // header
		0,    // source view offset
		12,   // source view length
		16,   // target view length
		7,    // instructions length
		1,    // new data length
		4, 0, // copy source, len 4, offset 0  -> "aaaa"
		4, 8, // copy source, len 4, offset 8  -> "cccc"
		0x81,    // copy new data, len 1          -> "d"
		0x47, 8, // copy target, len 7, offset 8 -> "ddddddd" (self-overlapping)
		'd', // new data
	}
	got, err := decodeSvndiff(source, doc)
	if err != nil {
		t.Fatalf("decodeSvndiff: %v", err)
	}
	if string(got) != "aaaaccccdddddddd" {
		t.Errorf("decodeSvndiff() = %q, want %q", got, "aaaaccccdddddddd")
	}
}

func TestEncodeSvndiff(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(""),
		[]byte("a"),
		[]byte("hello world\n"),
		[]byte("package main\n"),
		bytes.Repeat([]byte("x"), 63),  // exactly the 6-bit inline limit
		bytes.Repeat([]byte("x"), 64),  // just over it: forces the trailing-integer length form
		bytes.Repeat([]byte("y"), 300), // needs a multi-byte length varint
	}
	for _, content := range cases {
		t.Run(fmt.Sprintf("%d bytes", len(content)), func(t *testing.T) {
			doc := EncodeSvndiff(content)
			got, err := decodeSvndiff(nil, doc)
			if err != nil {
				t.Fatalf("decodeSvndiff(EncodeSvndiff(...)): %v", err)
			}
			if !bytes.Equal(got, content) {
				t.Errorf("round trip = %q, want %q", got, content)
			}
		})
	}
}

func TestEncodeSvndiffHeader(t *testing.T) {
	doc := EncodeSvndiff([]byte("x"))
	if len(doc) < 4 || string(doc[:4]) != "SVN\x00" {
		t.Errorf("EncodeSvndiff header = %q, want %q", doc[:min(4, len(doc))], "SVN\x00")
	}
}
