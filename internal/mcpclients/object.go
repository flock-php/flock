package mcpclients

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// object is a JSON object that keeps its keys in order, so a config file Flock edits only
// changes where Flock's entry is.
type object struct {
	keys   []string
	values map[string]json.RawMessage
}

func (o *object) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('{') {
		return errors.New("not a JSON object")
	}
	o.keys, o.values = nil, map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
		if _, dup := o.values[key]; !dup {
			o.keys = append(o.keys, key)
		}
		o.values[key] = value
	}
	_, err = dec.Token() // the closing brace
	return err
}

func (o *object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(key)
		b.Write(k)
		b.WriteByte(':')
		b.Write(o.values[key])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// set adds or replaces key, keeping its place when it exists.
func (o *object) set(key string, value json.RawMessage) {
	if o.values == nil {
		o.values = map[string]json.RawMessage{}
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// remove deletes key and reports whether it was there.
func (o *object) remove(key string) bool {
	if _, ok := o.values[key]; !ok {
		return false
	}
	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k string) bool { return k == key })
	return true
}

// object returns the object stored under key: nil when there is none, an error when key holds
// something else.
func (o *object) object(key string) (*object, error) {
	raw, ok := o.values[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return nil, nil
	}
	var inner object
	if err := json.Unmarshal(raw, &inner); err != nil {
		return nil, fmt.Errorf("%q isn't a JSON object", key)
	}
	return &inner, nil
}

func (o *object) setObject(key string, inner *object) error {
	data, err := json.Marshal(inner)
	if err != nil {
		return err
	}
	o.set(key, data)
	return nil
}
