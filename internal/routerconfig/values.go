package routerconfig

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/TwilightCoders/terraform-provider-alta-labs/internal/cloud"
)

// The put* helpers overlay one desired field onto a cloud object. A field whose current
// value already means the same thing is left byte-for-byte alone (9000 vs "9000"), so
// an unchanged configuration produces no writes. A zero desired value removes the field.

func putString(o cloud.Object, key, want string) {
	if want == "" {
		delete(o, key)
		return
	}
	if asString(o[key]) != want {
		o[key] = want
	}
}

// putPort stores a port the way the portal does: a number when it is a single port,
// a string for ranges.
func putPort(o cloud.Object, key, want string) {
	if want == "" {
		delete(o, key)
		return
	}
	if asString(o[key]) == want {
		return
	}
	if n, err := strconv.ParseInt(want, 10, 64); err == nil {
		o[key] = json.Number(strconv.FormatInt(n, 10))
		return
	}
	o[key] = want
}

func putInt(o cloud.Object, key string, want *int64) {
	if want == nil {
		delete(o, key)
		return
	}
	if got, ok := asInt(o[key]); !ok || got != *want {
		o[key] = json.Number(strconv.FormatInt(*want, 10))
	}
}

// putFlag stores true and removes the field otherwise, matching how the API omits
// false flags.
func putFlag(o cloud.Object, key string, want bool) {
	if !want {
		delete(o, key)
		return
	}
	if b, _ := o[key].(bool); !b {
		o[key] = true
	}
}

func putStrings(o cloud.Object, key string, want []string) {
	if len(want) == 0 {
		delete(o, key)
		return
	}
	if !equalStrings(asStrings(o[key]), want) {
		values := make([]any, len(want))
		for i, s := range want {
			values[i] = s
		}
		o[key] = values
	}
}

// putStringSet is putStrings for fields whose order carries no meaning: an existing
// value holding the same members is kept as it is.
func putStringSet(o cloud.Object, key string, want []string) {
	if sameMembers(asStrings(o[key]), want) {
		return
	}
	putStrings(o, key, want)
}

// putIntSet is the integer counterpart of putStringSet.
func putIntSet(o cloud.Object, key string, want []int64) {
	if len(want) == 0 {
		delete(o, key)
		return
	}
	if got, ok := asInts(o[key]); !ok || !sameMembers(got, want) {
		values := make([]any, len(want))
		for i, n := range want {
			values[i] = json.Number(strconv.FormatInt(n, 10))
		}
		o[key] = values
	}
}

// child returns the nested object at key, creating it when absent.
func child(o cloud.Object, key string) cloud.Object {
	if c, ok := o[key].(cloud.Object); ok {
		return c
	}
	c := cloud.Object{}
	o[key] = c
	return c
}

// pruneEmpty removes a nested object left with no fields.
func pruneEmpty(o cloud.Object, key string) {
	if c, ok := o[key].(cloud.Object); ok && len(c) == 0 {
		delete(o, key)
	}
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func asInt(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	case float64:
		return int64(t), t == float64(int64(t))
	default:
		return 0, false
	}
}

func asOptionalInt(v any) *int64 {
	if n, ok := asInt(v); ok {
		return &n
	}
	return nil
}

func asStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, asString(item))
	}
	return out
}

func asInts(v any) ([]int64, bool) {
	list, _ := v.([]any)
	out := make([]int64, 0, len(list))
	for _, item := range list {
		n, ok := asInt(item)
		if !ok {
			return nil, false
		}
		out = append(out, n)
	}
	return out, true
}

// splitCSV reads the comma-joined lists some site fields use.
func splitCSV(v any) []string {
	s := asString(v)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameMembers[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[T]int, len(a))
	for _, v := range a {
		count[v]++
	}
	for _, v := range b {
		if count[v] == 0 {
			return false
		}
		count[v]--
	}
	return true
}
