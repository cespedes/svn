package svn

import "fmt"

// This implements just enough of the svndiff0 format (see Subversion's
// own "notes/svndiff" design document) for this package's own needs:
// encoding ([EncodeSvndiff]) always produces a single window describing
// the content as entirely new data, with no copy from any source --
// enough for a command driving a full tree (e.g. a "checkout", where the
// server has nothing to diff against) -- while decoding (decodeSvndiff)
// handles a real server's own output too, including copy-from-source and
// copy-from-target instructions, for [Client.Checkout] to apply an
// incoming "apply-textdelta" against.
//
// An svndiff0 document is:
//
//	"SVN" version:byte
//	window ...
//
// and a window is the concatenation of five integers (source view
// offset, source view length, target view length, instructions section
// length in bytes, new data section length in bytes), each in svndiff's
// own variable-length encoding, followed by the instructions section and
// then the new data section.
//
// Each instruction byte's top 2 bits select copy-from-source (00),
// copy-from-target (01) or copy-from-new-data (10); the low 6 bits are
// the copy length, or 0 to mean "the length follows as an integer
// instead". Only copy-from-new-data is used here, and it takes no offset
// (each one implicitly continues from the last).

// svndiffPutUvarint appends v to buf using svndiff's variable-length
// integer encoding: base-128, most-significant group first, with the high
// bit of every byte but the last set to 1.
func svndiffPutUvarint(buf []byte, v uint64) []byte {
	var tmp [10]byte // ceil(64/7) = 10 groups, at most
	i := len(tmp)
	i--
	tmp[i] = byte(v & 0x7f)
	v >>= 7
	for v > 0 {
		i--
		tmp[i] = byte(v&0x7f) | 0x80
		v >>= 7
	}
	return append(buf, tmp[i:]...)
}

// EncodeSvndiff encodes content as a single svndiff0 document consisting
// of one window whose data is entirely new -- no copy from any source --
// suitable as the payload streamed via "apply-textdelta" (as its first
// chunk's data, possibly split across further "textdelta-chunk" calls) to
// describe a file that has nothing to diff against, e.g. while driving a
// full tree for a "checkout".
func EncodeSvndiff(content []byte) []byte {
	// A single "copy from new data" instruction covering the whole
	// content. Its length is always sent as a trailing integer (the low 6
	// bits of the instruction byte left at 0) rather than packed inline
	// for lengths under 64 -- a valid encoding either way, per the
	// format, but one fewer case to get right.
	var insns []byte
	if len(content) > 0 {
		insns = append(insns, 0x80)
		insns = svndiffPutUvarint(insns, uint64(len(content)))
	}

	out := append([]byte(nil), "SVN\x00"...)
	out = svndiffPutUvarint(out, 0)                    // source view offset
	out = svndiffPutUvarint(out, 0)                    // source view length
	out = svndiffPutUvarint(out, uint64(len(content))) // target view length
	out = svndiffPutUvarint(out, uint64(len(insns)))   // instructions length
	out = svndiffPutUvarint(out, uint64(len(content))) // new data length
	out = append(out, insns...)
	out = append(out, content...)
	return out
}

// svndiffGetUvarint reads one svndiff variable-length integer off the
// front of b, returning its value and the remaining, unconsumed bytes.
func svndiffGetUvarint(b []byte) (v uint64, rest []byte, err error) {
	for i, c := range b {
		v = v<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			return v, b[i+1:], nil
		}
	}
	return 0, nil, fmt.Errorf("svndiff: truncated integer")
}

// decodeSvndiff decodes an svndiff0 document (doc) into its target
// content, reading any copy-from-source instruction's source view
// directly out of source (the base content the window is diffing
// against; pass nil for a document with no source, e.g. one produced by
// [EncodeSvndiff]). It supports every instruction a real svnserve's own
// output uses -- copy-from-source, copy-from-target (which may overlap
// what the same instruction is still producing, for run-length-style
// repetition) and copy-from-new-data -- across as many windows as the
// document has, but each window's own source view is always read from
// source directly: unlike a real svndiff decoder, a later window's
// source view can't span data produced by an earlier window in the same
// document (real svndiff allows this, mainly to stream a huge file
// across multiple windows; nothing in this package ever needs to
// produce or consume that form, since every window this package's own
// EncodeSvndiff emits is self-contained).
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
