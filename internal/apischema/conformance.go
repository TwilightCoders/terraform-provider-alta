package apischema

import (
	"fmt"
	"sort"
	"strings"
)

// Conform reports where a capture disagrees with the schema: fields the schema does not
// know, fields whose type changed, and fields recorded as always present that are missing.
// It is how API drift surfaces as a test failure instead of a surprise at apply time.
func (s *Schema) Conform(captures ...Capture) []error {
	var problems []error
	for _, name := range s.objectNames() {
		obj := s.Objects[name]
		seen := map[string]int{}
		instances := 0
		for _, capture := range captures {
			found, err := capture.instances(obj.Source)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", name, err))
				continue
			}
			for _, inst := range found {
				instances++
				for key, value := range inst {
					seen[key]++
					field, known := obj.Fields[key]
					if !known {
						problems = append(problems, fmt.Errorf("%s: undescribed field %q (%s)", name, key, kindOf(value)))
						continue
					}
					if err := field.accepts(value); err != nil {
						problems = append(problems, fmt.Errorf("%s.%s: %w", name, key, err))
					}
				}
			}
		}
		for _, key := range sortedFieldNames(obj.Fields) {
			if !obj.Fields[key].Optional && instances > 0 && seen[key] < instances {
				problems = append(problems, fmt.Errorf("%s: field %q is described as always present but was missing from %d of %d instances",
					name, key, instances-seen[key], instances))
			}
		}
	}
	return problems
}

// accepts reports whether a value matches the field's observed shape.
func (f Field) accepts(value any) error {
	got := kindOf(value)
	if got == "null" {
		return nil // an explicit null is the API's way of saying unset
	}
	allowed := strings.Split(f.Type, "|")
	if f.Repr != "" {
		allowed = strings.Split(f.Repr, " or ")
	}
	for _, want := range allowed {
		if got == want || (strings.HasPrefix(want, "array<") && strings.HasPrefix(got, "array")) {
			return nil
		}
	}
	return fmt.Errorf("expected %s, got %s", strings.Join(allowed, " or "), got)
}

func sortedFieldNames(fields map[string]Field) []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
