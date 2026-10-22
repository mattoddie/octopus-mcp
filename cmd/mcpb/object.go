package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// object is a JSON object that keeps its keys in order, so that rewriting the
// hand-edited manifest changes only the values the tool sets. Values stay as
// raw JSON until they are needed.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(data []byte) (*object, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	o := &object{vals: map[string]json.RawMessage{}}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key := t.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if _, dup := o.vals[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		o.keys = append(o.keys, key)
		o.vals[key] = raw
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err == nil {
		return nil, errors.New("unexpected data after the object")
	}
	return o, nil
}

// get decodes the value of key into v, reporting whether the key exists.
func (o *object) get(key string, v any) (bool, error) {
	raw, ok := o.vals[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, v)
}

// child returns the object stored under key, or nil if there is none.
func (o *object) child(key string) (*object, error) {
	raw, ok := o.vals[key]
	if !ok {
		return nil, nil
	}
	c, err := parseObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return c, nil
}

// set stores v under key, keeping the key's position or appending it.
func (o *object) set(key string, v any) error {
	var raw []byte
	if c, ok := v.(*object); ok {
		var err error
		if raw, err = c.MarshalJSON(); err != nil {
			return err
		}
	} else {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		raw = bytes.TrimSpace(buf.Bytes())
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
	return nil
}

func (o *object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		name, _ := json.Marshal(k)
		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(o.vals[k])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// format renders the object indented by two spaces, with a final newline.
func (o *object) format() (string, error) {
	raw, err := o.MarshalJSON()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return "", err
	}
	return buf.String() + "\n", nil
}
