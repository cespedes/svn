package svn

import (
	"bytes"
	"fmt"
	"testing"
)

// The rest of this file is a from-scratch, independent svndiff0 decoder,
// used only to verify EncodeSvndiff: first against the format spec's own
// worked example (byte-for-byte, taken from Subversion's
// "notes/svndiff"), and then to round-trip EncodeSvndiff's own output.
// It only needs to handle what that worked example and EncodeSvndiff's
// output actually use: a single window, reading its source view directly
// from the "source" argument (real svndiff lets a later window's source
// view span previously-produced target data too, which this does not
// implement, since nothing here ever needs it).

func svndiffGetUvarint(b []byte) (v uint64, rest []byte, err error) {
	for i, c := range b {
		v = v<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			return v, b[i+1:], nil
		}
	}
	return 0, nil, fmt.Errorf("svndiff: truncated integer")
}

func decodeSvndiff(source, doc []byte) ([]byte, error) {
	if len(doc) < 4 || string(doc[:3]) != "SVN" {
		return nil, fmt.Errorf("svndiff: bad header")
	}
	if doc[3] != 0 {
		return nil, fmt.Errorf("svndiff: unsupported version %d", doc[3])
	}
	rest := doc[4:]

	var target []byte
	for len(rest) > 0 {
		var srcOff, srcLen, tgtLen, insnLen, dataLen uint64
		var err error
		for _, p := range []*uint64{&srcOff, &srcLen, &tgtLen, &insnLen, &dataLen} {
			*p, rest, err = svndiffGetUvarint(rest)
			if err != nil {
				return nil, err
			}
		}
		if uint64(len(rest)) < insnLen+dataLen {
			return nil, fmt.Errorf("svndiff: truncated window")
		}
		insns := rest[:insnLen]
		data := rest[insnLen : insnLen+dataLen]
		rest = rest[insnLen+dataLen:]

		if srcOff+srcLen > uint64(len(source)) {
			return nil, fmt.Errorf("svndiff: source view out of range")
		}
		srcView := source[srcOff : srcOff+srcLen]

		windowStart := len(target)
		var dataPos uint64
		for len(insns) > 0 {
			b0 := insns[0]
			insns = insns[1:]
			selector := b0 >> 6
			length := uint64(b0 & 0x3f)
			if length == 0 {
				length, insns, err = svndiffGetUvarint(insns)
				if err != nil {
					return nil, err
				}
			}
			switch selector {
			case 0: // copy from source view
				var off uint64
				off, insns, err = svndiffGetUvarint(insns)
				if err != nil {
					return nil, err
				}
				if off+length > uint64(len(srcView)) {
					return nil, fmt.Errorf("svndiff: source copy out of range")
				}
				target = append(target, srcView[off:off+length]...)
			case 1: // copy from target view (may overlap what this same instruction is producing)
				var off uint64
				off, insns, err = svndiffGetUvarint(insns)
				if err != nil {
					return nil, err
				}
				start := windowStart + int(off)
				for i := uint64(0); i < length; i++ {
					if start+int(i) >= len(target) {
						return nil, fmt.Errorf("svndiff: target copy out of range")
					}
					target = append(target, target[start+int(i)])
				}
			case 2: // copy from new data
				if dataPos+length > uint64(len(data)) {
					return nil, fmt.Errorf("svndiff: new data out of range")
				}
				target = append(target, data[dataPos:dataPos+length]...)
				dataPos += length
			default:
				return nil, fmt.Errorf("svndiff: invalid instruction selector")
			}
		}
		if uint64(len(target)-windowStart) != tgtLen {
			return nil, fmt.Errorf("svndiff: target view length mismatch: got %d, want %d", len(target)-windowStart, tgtLen)
		}
	}
	return target, nil
}

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
