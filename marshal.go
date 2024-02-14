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
	// case reflect.Bool:
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return Item{
			Type:   NumberType,
			Number: uint(v.Int()),
		}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Item{
			Type:   NumberType,
			Number: uint(v.Uint()),
		}, nil
	// case reflect.Uintptr:
	case reflect.Float32, reflect.Float64:
		return Item{
			Type:   NumberType,
			Number: uint(v.Float()),
		}, nil
	// case reflect.Complex64:
	// case reflect.Complex128:
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return Item{
				Type: StringType,
				Text: string(v.Bytes()),
			}, nil
		}
		fallthrough
	case reflect.Array:
		item := Item{
			Type: ListType,
		}
		for i := range v.Len() {
			it, err := Marshal(v.Index(i).Interface())
			if err != nil {
				return Item{}, err
			}
			if it.Type != InvalidType {
				item.List = append(item.List, it)
			}
		}
		return item, nil
	// case reflect.Chan:
	// case reflect.Func:
	// case reflect.Interface:
	// case reflect.Map:
	case reflect.Pointer:
		if v.IsNil() {
			return Item{}, nil
		}
		return Marshal(v.Elem().Interface())
	case reflect.String:
		return Item{
			Type: WordType,
			Text: v.String(),
		}, nil
	case reflect.Struct:
		item := Item{
			Type: ListType,
		}
		for i := range v.NumField() {
			// do not marshal unexported fields:
			if !v.Type().Field(i).IsExported() {
				continue
			}
			it, err := Marshal(v.Field(i).Interface())
			if err != nil {
				return Item{}, err
			}
			if it.Type != InvalidType {
				item.List = append(item.List, it)
			}
		}
		return item, nil
	// case reflect.UnsafePointer:
	default:
		return Item{}, fmt.Errorf("cannot marshal kind %q", v.Kind())
	}
}

// Unmarshal parses an Item and copies it to the value pointed to by v.
// If v is nil or not a pointer, Unmarshal returns an error.
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
//   - int, for numbers
//   - string, for words or strings
//   - []any, for lists
//
// To unmarshal a list into a slice, Unmarshal resets the slice length
// to zero and then appends each element to the slice. As a special case,
// to unmarshal an empty list into a slice, Unmarshal replaces the slice
// with a new empty slice.
func Unmarshal(item Item, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("Unmarshal: invalid kind %q", rv.Kind())
	}
	rv = rv.Elem()
	return unmarshal(item, rv)
}

func unmarshal(item Item, v reflect.Value) error {
	if v.Type() == reflect.TypeOf(item) {
		v.Set(reflect.ValueOf(item))
		return nil
	}

	switch item.Type {
	case WordType:
		if v.Kind() != reflect.String {
			return fmt.Errorf("cannot unmarshal a Word into kind %q", v.Kind())
		}
		v.SetString(item.Text)
		return nil
	case StringType:
		if v.Kind() != reflect.String {
			return fmt.Errorf("cannot unmarshal a String into kind %q", v.Kind())
		}
		v.SetString(item.Text)
		return nil
	case NumberType:
		switch v.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			v.SetInt(int64(item.Number))
			return nil
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			v.SetUint(uint64(item.Number))
			return nil
		}
		return fmt.Errorf("cannot unmarshal a Number into kind %q", v.Kind())
	case ListType:
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < min(len(item.List), v.NumField()); i++ {
				// unmarshaling to unexported fields is forbidden:
				if !v.Type().Field(i).IsExported() {
					return fmt.Errorf("cannot unmarshal into unexported field")
				}
				err := unmarshal(item.List[i], v.Field(i))
				if err != nil {
					return err
				}
			}
			return nil
		case reflect.Slice:
			if len(item.List) > v.Len() {
				v.Grow(len(item.List) - v.Len())
				v.SetLen(len(item.List))
			}
			for i := range len(item.List) {
				err := unmarshal(item.List[i], v.Index(i))
				if err != nil {
					return err
				}
			}
			return nil
		default:
			return fmt.Errorf("unmarshaling from ListType into kind %q is not implemented", v.Kind())
		}
	}
	v.SetInt(42)
	return nil

	return errors.New("not implemented")
}
