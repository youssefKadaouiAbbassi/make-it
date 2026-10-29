// Package jsonx reads and writes JSON while keeping object key order and number text,
// so a file makeit rewrites keeps the shape the user and Claude Code gave it.
//
// Values are nil, bool, json.Number, string, []any and *Object.
package jsonx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Object is a JSON object with its key order.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{vals: map[string]any{}} }

// Keys returns the keys in order. The slice must not be modified.
func (o *Object) Keys() []string { return o.keys }

// Len is the number of members.
func (o *Object) Len() int { return len(o.keys) }

// Get returns the member and whether it exists.
func (o *Object) Get(k string) (any, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// Set adds or replaces a member; a new key goes last, an existing key keeps its place.
func (o *Object) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

// Delete removes a member if present.
func (o *Object) Delete(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Decode parses exactly one JSON value. Duplicate keys keep the first position and the last value,
// the way JSON.parse and Claude Code read them.
func Decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("jsonx: data after the JSON value")
	}
	return v, nil
}

// DecodeObject parses a JSON object; empty or whitespace-only input is an empty object.
func DecodeObject(data []byte) (*Object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return NewObject(), nil
	}
	v, err := Decode(data)
	if err != nil {
		return nil, err
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, errors.New("jsonx: not a JSON object")
	}
	return o, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("jsonx: object key %v is not a string", kt)
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(k, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("jsonx: unexpected %v", t)
	default:
		return tok, nil
	}
}

// Marshal writes v with two-space indentation and a trailing newline, without HTML escaping:
// the shape Claude Code writes settings.json in.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := write(&b, v, "  ", ""); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// Compact writes v on one line, keys in their order.
func Compact(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := write(&b, v, "", ""); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeString(b *bytes.Buffer, s string) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(tmp.Bytes(), []byte("\n")))
	return nil
}

func write(b *bytes.Buffer, v any, indent, prefix string) error {
	nl := func(p string) {
		if indent != "" {
			b.WriteByte('\n')
			b.WriteString(p)
		}
	}
	colon := ":"
	if indent != "" {
		colon = ": "
	}
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		b.WriteString(t.String())
	case string:
		return writeString(b, t)
	case int:
		fmt.Fprintf(b, "%d", t)
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(prefix + indent)
			if err := write(b, e, indent, prefix+indent); err != nil {
				return err
			}
		}
		nl(prefix)
		b.WriteByte(']')
	case *Object:
		if t.Len() == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(prefix + indent)
			if err := writeString(b, k); err != nil {
				return err
			}
			b.WriteString(colon)
			if err := write(b, t.vals[k], indent, prefix+indent); err != nil {
				return err
			}
		}
		nl(prefix)
		b.WriteByte('}')
	default:
		return fmt.Errorf("jsonx: unsupported type %T", v)
	}
	return nil
}

// Canonical is a compact form with object keys sorted. Claude Code reorders keys on every write,
// so equality and drift are decided on this form, never on bytes.
func Canonical(v any) string {
	var b strings.Builder
	canon(&b, v)
	return b.String()
}

func canon(b *strings.Builder, v any) {
	switch t := v.(type) {
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			canon(b, e)
		}
		b.WriteByte(']')
	case *Object:
		keys := append([]string(nil), t.keys...)
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			var kb bytes.Buffer
			_ = writeString(&kb, k)
			b.Write(kb.Bytes())
			b.WriteByte(':')
			canon(b, t.vals[k])
		}
		b.WriteByte('}')
	default:
		var vb bytes.Buffer
		if err := write(&vb, v, "", ""); err != nil {
			b.WriteString("<invalid>")
			return
		}
		b.Write(vb.Bytes())
	}
}

// Equal compares two values in canonical form.
func Equal(a, b any) bool { return Canonical(a) == Canonical(b) }

// Hash is the sha256 of the canonical form, as "sha256:<hex>".
func Hash(v any) string {
	sum := sha256.Sum256([]byte(Canonical(v)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Clone deep-copies a value.
func Clone(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = Clone(e)
		}
		return out
	case *Object:
		o := NewObject()
		for _, k := range t.keys {
			o.Set(k, Clone(t.vals[k]))
		}
		return o
	default:
		return v
	}
}

// Value wraps a jsonx value so it can sit inside structs encoded with encoding/json.
type Value struct{ V any }

// MarshalJSON implements json.Marshaler.
func (v Value) MarshalJSON() ([]byte, error) { return Compact(v.V) }

// UnmarshalJSON implements json.Unmarshaler.
func (v *Value) UnmarshalJSON(data []byte) error {
	x, err := Decode(data)
	if err != nil {
		return err
	}
	v.V = x
	return nil
}
