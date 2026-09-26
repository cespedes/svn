package svn

// This implements just enough of the svndiff0 format (see Subversion's
// own "notes/svndiff" design document) to encode file content for the
// protocol's "apply-textdelta"/"textdelta-chunk"/"textdelta-end" commands:
// a single window describing the content as entirely new data, with no
// copy from any source. That's all a command driving a full tree (e.g. a
// "checkout", where the client has nothing to diff against) ever needs;
// it deliberately doesn't implement real delta compression against a
// source, or emitting more than one window.
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
