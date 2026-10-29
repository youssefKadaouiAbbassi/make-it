package jsonx

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoundTripKeepsOrderNumbersAndText(t *testing.T) {
	in := `{
  "z": 9007199254740993,
  "a": 1.0,
  "cmd": "a && b <x>",
  "nested": {
    "y": [],
    "x": {}
  },
  "list": [
    true,
    null,
    "s"
  ]
}
`
	v, err := Decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Fatalf("round trip changed the text:\n%s", out)
	}
}

func TestDuplicateKeyKeepsFirstPlaceLastValue(t *testing.T) {
	o, err := DecodeObject([]byte(`{"a":1,"b":2,"a":3}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(o.Keys(), ","); got != "a,b" {
		t.Fatalf("keys %s", got)
	}
	if v, _ := o.Get("a"); v != json.Number("3") {
		t.Fatalf("a = %v", v)
	}
}

func TestDecodeErrors(t *testing.T) {
	for _, in := range []string{`{"a":1} {}`, `{"a":`, `[1,]`, `nul`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
	if _, err := DecodeObject([]byte(`[1]`)); err == nil {
		t.Error("array accepted as object")
	}
	if o, err := DecodeObject([]byte("  \n")); err != nil || o.Len() != 0 {
		t.Errorf("blank input: %v %v", o, err)
	}
}

func TestCanonicalIgnoresKeyOrder(t *testing.T) {
	a, _ := Decode([]byte(`{"b":{"y":1,"x":[2,{"q":1,"p":2}]},"a":"&"}`))
	b, _ := Decode([]byte(`{"a":"&","b":{"x":[2,{"p":2,"q":1}],"y":1}}`))
	if !Equal(a, b) || Hash(a) != Hash(b) {
		t.Fatalf("%s != %s", Canonical(a), Canonical(b))
	}
	c, _ := Decode([]byte(`{"a":"&","b":{"x":[{"p":2,"q":1},2],"y":1}}`))
	if Equal(a, c) {
		t.Fatal("array order must matter")
	}
	if Canonical(a) != `{"a":"&","b":{"x":[2,{"p":2,"q":1}],"y":1}}` {
		t.Fatalf("canonical %s", Canonical(a))
	}
}

func TestObjectEditing(t *testing.T) {
	o := NewObject()
	o.Set("a", 1)
	o.Set("b", 2)
	o.Set("a", 3)
	o.Delete("missing")
	o.Delete("a")
	o.Set("a", 4)
	if got := strings.Join(o.Keys(), ","); got != "b,a" {
		t.Fatalf("keys %s", got)
	}
	cl := Clone(o).(*Object)
	cl.Set("c", 5)
	if o.Len() != 2 {
		t.Fatal("clone shares storage")
	}
}

func TestValueInsideEncodingJSON(t *testing.T) {
	type rec struct {
		V Value `json:"v"`
	}
	var r rec
	if err := json.Unmarshal([]byte(`{"v":{"z":1,"a":[1.50]}}`), &r); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"v":{"z":1,"a":[1.50]}}` {
		t.Fatalf("%s", b)
	}
}
