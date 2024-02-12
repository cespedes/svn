package svn

import (
	"errors"
	"fmt"
	"reflect"
)

//	WordType ItemType = iota
//	NumberType
//	StringType
//	ListType

// Marshal converts v into an Item.
//
// Marshal traverses the value v recursively.
//
// Floating point and integer values encode as numbers.
//
// String values encode as words.
//
// Array, slice and struct values encode as lists, except that []byte
// values encode as strings.
func Marshal(v any) (Item, error) {
	switch v := reflect.ValueOf(v); v.Kind() {
	case reflect.Bool:
	case reflect.Int:
		fallthrough
	case reflect.Int8:
		fallthrough
	case reflect.Int16:
		fallthrough
	case reflect.Int32:
		fallthrough
	case reflect.Int64:
		return Item{
			Type:   NumberType,
			Number: uint(v.Int()),
		}, nil
	case reflect.Uint:
		fallthrough
	case reflect.Uint8:
		fallthrough
	case reflect.Uint16:
		fallthrough
	case reflect.Uint32:
		fallthrough
	case reflect.Uint64:
		return Item{
			Type:   NumberType,
			Number: uint(v.Uint()),
		}, nil
	case reflect.Float32:
		fallthrough
	case reflect.Float64:
		return Item{
			Type:   NumberType,
			Number: uint(v.Float()),
		}, nil
	case reflect.String:
		return Item{
			Type: WordType,
			Word: v.String(),
		}, nil
	case reflect.Uintptr:
	case reflect.Complex64:
	case reflect.Complex128:
	case reflect.Array:
	case reflect.Chan:
	case reflect.Func:
	case reflect.Interface:
	case reflect.Map:
	case reflect.Pointer:
	case reflect.Slice:
	case reflect.Struct:
	case reflect.UnsafePointer:
	default:
		return Item{}, fmt.Errorf("unhandled kind %s", v.Kind())
	}
	return Item{}, errors.New("not implemented")
}

// Unmarshal parses the JSON-encoded data and stores the result in the
// value pointed to by v. If v is nil or not a pointer, Unmarshal returns an
// InvalidUnmarshalError.
//
// Unmarshal uses the inverse of the encodings that Marshal uses, allocating
// slices and pointers as necessary, with the following additional
// rules:
//
// To unmarshal an Item into a pointer, Unmarshal unmarshals the Item
// into the value pointed at by the pointer.
// If the pointer is nil, Unmarshal allocates a new value for it to point to.
//
// To unmarshal a list Item into a struct, Unmarshal matches the values
// in the same order as they are declared in the struct.  If there are extra
// fields in the struct, they are ignored.
//
// To unmarshal an Item into an interface value, Unmarshal stores one of these
// in the interface value:
//
//   - int, for integers
//   - string, for words
//   - []byte, for strings
//   - []any, for lists
//
// To unmarshal a list into a slice, Unmarshal resets the slice length
// to zero and then appends each element to the slice. As a special case,
// to unmarshal an empty list into a slice, Unmarshal replaces the slice
// with a new empty slice.
func Unmarshal(item Item, v any) error {
	return errors.New("not implemented")
}
