package apischema

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

// Capture is one set of API responses to fit or check a schema against.
type Capture struct {
	Site    map[string]any
	Device  map[string]any
	Clients []map[string]any
}

// enumerable are fields whose values form a closed vocabulary worth recording. Free text
// (names, descriptions, addresses) is deliberately excluded: the schema describes shapes,
// never the contents of anyone's site.
var enumerable = map[string]bool{
	"action": true, "type": true, "ipVersion": true, "protocol": true,
	"mode": true, "speed": true, "eee": true, "icmpType": true,
}

// Fit derives object field inventories from captures, keeping each object's Source.
func (s *Schema) Fit(captures ...Capture) error {
	for _, name := range s.objectNames() {
		obj := s.Objects[name]
		fields := map[string]*fitted{}
		instances := 0
		for _, capture := range captures {
			found, err := capture.instances(obj.Source)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			for _, inst := range found {
				instances++
				for key, value := range inst {
					f, ok := fields[key]
					if !ok {
						f = &fitted{}
						fields[key] = f
					}
					f.observe(key, value)
				}
			}
		}
		switch {
		case obj.Unobserved && instances > 0:
			return fmt.Errorf("%s: marked unobserved but %d instances found at %q; fit them and drop the flag", name, instances, obj.Source)
		case obj.Unobserved:
			continue
		case instances == 0:
			return fmt.Errorf("%s: no instances at %q", name, obj.Source)
		}
		for _, f := range fields {
			f.optional = f.count < instances
		}
		if !obj.PartialCapture {
			obj.Fields = map[string]Field{}
		}
		if obj.Fields == nil {
			obj.Fields = map[string]Field{}
		}
		for key, f := range fields {
			obj.Fields[key] = f.field()
		}
		s.Objects[name] = obj
	}
	return nil
}

// fitted accumulates what was seen for one field.
type fitted struct {
	types    map[string]bool
	values   map[string]bool
	count    int
	optional bool
}

func (f *fitted) observe(key string, value any) {
	if f.types == nil {
		f.types, f.values = map[string]bool{}, map[string]bool{}
	}
	f.count++
	f.types[kindOf(value)] = true
	if !enumerable[key] {
		return
	}
	for _, v := range flatten(value) {
		f.values[v] = true
	}
}

func (f *fitted) field() Field {
	types := keys(f.types)
	out := Field{Type: strings.Join(types, "|"), Optional: f.optional}
	if len(types) > 1 {
		out.Type = types[0]
		out.Repr = strings.Join(types, " or ")
	}
	if len(f.values) > 0 {
		out.Values = keys(f.values)
	}
	return out
}

func kindOf(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case string:
		return "string"
	case json.Number, float64:
		return "number"
	case []any:
		kinds := map[string]bool{}
		for _, item := range t {
			kinds[kindOf(item)] = true
		}
		if len(kinds) == 0 {
			return "array"
		}
		return "array<" + strings.Join(keys(kinds), "|") + ">"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func flatten(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, item := range t {
			out = append(out, flatten(item)...)
		}
		return out
	default:
		return nil
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// instances resolves a source path such as "site:firewall.nat.rules[]",
// "device:portsCfg.ports{}" or "client:config" to the objects it selects.
func (c Capture) instances(source string) ([]map[string]any, error) {
	root, path, ok := strings.Cut(source, ":")
	if !ok {
		return nil, fmt.Errorf("source %q must start with site:, device: or client", source)
	}
	var nodes []any
	switch root {
	case "site":
		nodes = []any{c.Site}
	case "device":
		nodes = []any{c.Device}
	case "client":
		for _, client := range c.Clients {
			nodes = append(nodes, client)
		}
	default:
		return nil, fmt.Errorf("unknown source root %q", root)
	}

	for _, step := range strings.Split(path, ".") {
		var next []any
		for _, node := range nodes {
			obj, ok := node.(map[string]any)
			if !ok {
				continue
			}
			switch {
			case strings.HasSuffix(step, "[]"):
				list, _ := obj[strings.TrimSuffix(step, "[]")].([]any)
				next = append(next, list...)
			case strings.HasSuffix(step, "{}"):
				m, _ := obj[strings.TrimSuffix(step, "{}")].(map[string]any)
				for _, k := range sortedKeys(m) {
					next = append(next, m[k])
				}
			default:
				if v, ok := obj[step]; ok && v != nil {
					next = append(next, v)
				}
			}
		}
		nodes = next
	}

	out := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		if obj, ok := node.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out, nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CaptureFrom assembles a Capture from a site read and its live state.
func CaptureFrom(site cloud.Object, state cloud.State, deviceID string) (Capture, error) {
	device, ok := state.Device(deviceID)
	if !ok {
		return Capture{}, fmt.Errorf("site has no device %q", deviceID)
	}
	return Capture{Site: site, Device: device, Clients: append([]cloud.Object(nil), state.Clients...)}, nil
}
