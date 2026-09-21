package plugin

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"time"
)

var durationType = reflect.TypeFor[time.Duration]()

// assignValue writes one raw parameter value into a struct field.
func assignValue(field reflect.Value, raw any, key string) error {
	if raw == nil {
		return fmt.Errorf("parameter %q has no value", key)
	}

	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}

		return assignValue(field.Elem(), raw, key)
	}

	rawValue := reflect.ValueOf(raw)
	if rawValue.Type() == field.Type() {
		// Copy, so that the configuration does not alias the caller's map:
		// the caller may reuse it, for example to merge definitions.
		field.Set(deepCopy(rawValue))

		return nil
	}

	if field.Type() == durationType {
		return assignDuration(field, raw, key)
	}

	if u, ok := textUnmarshaler(field); ok {
		text, ok := raw.(string)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}
		if err := u.UnmarshalText([]byte(text)); err != nil {
			return fmt.Errorf("parameter %q: %w", key, err)
		}

		return nil
	}

	switch field.Kind() {
	case reflect.Slice:
		return assignSlice(field, raw, key)
	case reflect.Map:
		return assignMap(field, raw, key)
	case reflect.Struct:
		nested, ok := raw.(map[string]any)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}

		return decodeStruct(field, nested, key+".")
	default:
		return assignBasic(field, raw, key)
	}
}

// deepCopy returns a copy of v that shares no slice, map or pointer with it.
// Structs are copied by value, which is enough for the types a TOML parser
// produces.
func deepCopy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Slice:
		if v.IsNil() {
			return v
		}

		clone := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			clone.Index(i).Set(deepCopy(v.Index(i)))
		}

		return clone
	case reflect.Map:
		if v.IsNil() {
			return v
		}

		clone := reflect.MakeMapWithSize(v.Type(), v.Len())
		for iter := v.MapRange(); iter.Next(); {
			clone.SetMapIndex(iter.Key(), deepCopy(iter.Value()))
		}

		return clone
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}

		clone := reflect.New(v.Type().Elem())
		clone.Elem().Set(deepCopy(v.Elem()))

		return clone
	case reflect.Interface:
		if v.IsNil() {
			return v
		}

		clone := reflect.New(v.Type()).Elem()
		clone.Set(deepCopy(v.Elem()))

		return clone
	default:
		return v
	}
}

// assignDuration parses a duration written as a string, such as "10s".
func assignDuration(field reflect.Value, raw any, key string) error {
	text, ok := raw.(string)
	if !ok {
		return typeMismatch(key, raw, field.Type())
	}

	d, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("parameter %q: %w", key, err)
	}
	field.SetInt(int64(d))

	return nil
}

// assignBasic writes a string, boolean or number into a field of that kind.
func assignBasic(field reflect.Value, raw any, key string) error {
	switch field.Kind() {
	case reflect.String:
		text, ok := raw.(string)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}
		field.SetString(text)
	case reflect.Bool:
		b, ok := raw.(bool)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}
		field.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := toInt(raw)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}
		if field.OverflowInt(n) {
			return fmt.Errorf("parameter %q: value %d is out of range for %s", key, n, field.Type())
		}
		field.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := toInt(raw)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}
		if n < 0 || field.OverflowUint(uint64(n)) {
			return fmt.Errorf("parameter %q: value %d is out of range for %s", key, n, field.Type())
		}
		field.SetUint(uint64(n))
	case reflect.Float32, reflect.Float64:
		f, ok := toFloat(raw)
		if !ok {
			return typeMismatch(key, raw, field.Type())
		}
		if field.OverflowFloat(f) {
			return fmt.Errorf("parameter %q: value %v is out of range for %s", key, f, field.Type())
		}
		field.SetFloat(f)
	default:
		return fmt.Errorf("parameter %q: unsupported type %s", key, field.Type())
	}

	return nil
}

// assignSlice writes a TOML array into a slice field.
func assignSlice(field reflect.Value, raw any, key string) error {
	items, ok := raw.([]any)
	if !ok {
		return typeMismatch(key, raw, field.Type())
	}

	slice := reflect.MakeSlice(field.Type(), len(items), len(items))
	for i, item := range items {
		if err := assignValue(slice.Index(i), item, fmt.Sprintf("%s[%d]", key, i)); err != nil {
			return err
		}
	}
	field.Set(slice)

	return nil
}

// assignMap writes a TOML table into a map field with string keys.
func assignMap(field reflect.Value, raw any, key string) error {
	if field.Type().Key().Kind() != reflect.String {
		return fmt.Errorf("parameter %q: unsupported type %s, map keys must be strings", key, field.Type())
	}

	table, ok := raw.(map[string]any)
	if !ok {
		return typeMismatch(key, raw, field.Type())
	}

	result := reflect.MakeMapWithSize(field.Type(), len(table))
	for name, value := range table {
		elem := reflect.New(field.Type().Elem()).Elem()
		if err := assignValue(elem, value, key+"."+name); err != nil {
			return err
		}
		result.SetMapIndex(reflect.ValueOf(name).Convert(field.Type().Key()), elem)
	}
	field.Set(result)

	return nil
}

// textUnmarshaler reports whether a field can parse itself from text.
func textUnmarshaler(field reflect.Value) (encoding.TextUnmarshaler, bool) {
	if !field.CanAddr() {
		return nil, false
	}

	u, ok := field.Addr().Interface().(encoding.TextUnmarshaler)

	return u, ok
}

// toInt accepts the integer types a TOML parser can produce.
func toInt(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case int32:
		return int64(v), true
	default:
		return 0, false
	}
}

// toFloat accepts floating point values and integers, since TOML writes whole
// numbers without a decimal point.
func toFloat(raw any) (float64, bool) {
	if f, ok := raw.(float64); ok {
		return f, true
	}
	if n, ok := toInt(raw); ok {
		return float64(n), true
	}

	return 0, false
}

// typeMismatch reports a value whose type does not fit the field.
func typeMismatch(key string, raw any, target reflect.Type) error {
	return fmt.Errorf("parameter %q: cannot assign %T to %s", key, raw, target)
}

// setFromString applies a `default` tag, which is always written as text.
func setFromString(field reflect.Value, raw string) error {
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}

		return setFromString(field.Elem(), raw)
	}

	if field.Type() == durationType {
		d, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}
		field.SetInt(int64(d))

		return nil
	}

	if u, ok := textUnmarshaler(field); ok {
		return u.UnmarshalText([]byte(raw))
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(raw)
	case reflect.Bool:
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return err
		}
		field.SetBool(v)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(v)
	default:
		return fmt.Errorf("default values are not supported for type %s", field.Type())
	}

	return nil
}
