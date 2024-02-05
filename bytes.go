package svn

var asciiSpace = [256]bool{
	'\t': true,
	'\n': true,
	'\v': true,
	'\f': true,
	'\r': true,
	' ':  true,
}

func isspace(b byte) bool {
	return asciiSpace[b]
}

func isalpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isnum(b byte) bool {
	return b >= '0' && b <= '9'
}

func isalnum(b byte) bool {
	return isalpha(b) || isnum(b)
}
