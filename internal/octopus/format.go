package octopus

import (
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Obj is a JSON object from the API. Objects are passed around generically
// so fields this server doesn't know about survive into raw output.
type Obj = map[string]any

// Compact drops nil, "", empty slices/maps and false booleans so summaries stay small.
func Compact(o Obj) Obj {
	for k, v := range o {
		switch x := v.(type) {
		case nil:
			delete(o, k)
		case string:
			if x == "" {
				delete(o, k)
			}
		case bool:
			if !x {
				delete(o, k)
			}
		case []any:
			if len(x) == 0 {
				delete(o, k)
			}
		case []string:
			if len(x) == 0 {
				delete(o, k)
			}
		case []Obj:
			if len(x) == 0 {
				delete(o, k)
			}
		case Obj:
			if len(x) == 0 {
				delete(o, k)
			}
		}
	}
	return o
}

// Get walks nested objects: Get(o, "tariff", "productCode").
func Get(o any, path ...string) any {
	cur := o
	for _, p := range path {
		m, ok := cur.(Obj)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	return cur
}

// Str returns a nested string (or a number formatted as a string), or "".
func Str(o any, path ...string) string {
	switch v := Get(o, path...).(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// Bool returns a nested boolean, or false.
func Bool(o any, path ...string) bool {
	b, _ := Get(o, path...).(bool)
	return b
}

// Num returns a nested number, accepting numbers and decimal strings.
func Num(o any, path ...string) (float64, bool) {
	switch v := Get(o, path...).(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

// Pence converts a nested integer amount in pence to pounds, or nil when absent.
func Pence(o any, path ...string) any {
	f, ok := Num(o, path...)
	if !ok {
		return nil
	}
	return Round(f/100, 2)
}

// NumOrNil returns a nested number rounded to the given places, or nil when absent.
func NumOrNil(o any, places int, path ...string) any {
	f, ok := Num(o, path...)
	if !ok {
		return nil
	}
	return Round(f, places)
}

// Round rounds to the given number of decimal places.
func Round(f float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(f*p) / p
}

// List returns a nested array of objects.
func List(o any, path ...string) []Obj {
	arr, _ := Get(o, path...).([]any)
	out := make([]Obj, 0, len(arr))
	for _, x := range arr {
		if m, ok := x.(Obj); ok {
			out = append(out, m)
		}
	}
	return out
}

// Edges returns the nodes of a GraphQL connection ({edges: [{node: ...}]}).
func Edges(o any, path ...string) []Obj {
	var out []Obj
	for _, e := range List(o, append(path, "edges")...) {
		if n, ok := e["node"].(Obj); ok {
			out = append(out, n)
		}
	}
	return out
}

// SortBy sorts objects by a string field, case-insensitively.
func SortBy(items []Obj, field string) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(Str(items[i], field)) < strings.ToLower(Str(items[j], field))
	})
}
