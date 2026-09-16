package routerconfig

import (
	"fmt"
	"slices"
	"strings"

	"github.com/TwilightCoders/terraform-provider-alta/internal/cloud"
)

// listCodec maps identified items to a JSON array of objects.
//
// Writes are authoritative: the array holds exactly the given items. An item whose id
// already exists is overlaid onto that element, so fields the codec does not model are
// kept. When ordered, the array takes the given order; otherwise existing elements keep
// their position and new ones are appended, so reordering the input changes nothing.
type listCodec[T any] struct {
	// path locates the array from the document root, e.g. firewall → nat → rules.
	path    []string
	ordered bool
	id      func(T) string
	// decode reads an item from an element.
	decode func(cloud.Object) T
	// encode overlays an item onto an element (fresh or existing).
	encode func(T, cloud.Object)
	// sort, when set, writes the array in this order rather than keeping current
	// positions. Use it where the portal itself rewrites the whole array in a fixed
	// order, so an edit here and an edit in the UI do not reshuffle each other.
	sort func(a, b T) int
}

func (c listCodec[T]) read(root cloud.Object) []T {
	elements := c.elements(root)
	items := make([]T, 0, len(elements))
	for _, e := range elements {
		items = append(items, c.decode(e))
	}
	return items
}

func (c listCodec[T]) write(root cloud.Object, items []T) error {
	current := c.elements(root)
	existing := make(map[string]cloud.Object, len(current))
	for _, e := range current {
		existing[asString(e["id"])] = e
	}

	byID := make(map[string]T, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		id := c.id(item)
		if id == "" {
			return fmt.Errorf("%s: every item needs an id", c.name())
		}
		if _, dup := byID[id]; dup {
			return fmt.Errorf("%s: duplicate id %q", c.name(), id)
		}
		byID[id] = item
		order = append(order, id)
	}
	switch {
	case c.sort != nil:
		sorted := slices.Clone(items)
		slices.SortStableFunc(sorted, c.sort)
		order = order[:0]
		for _, item := range sorted {
			order = append(order, c.id(item))
		}
	case !c.ordered:
		order = stableOrder(current, order)
	}

	next := make([]any, 0, len(order))
	for _, id := range order {
		element, ok := existing[id]
		if !ok {
			element = cloud.Object{}
		}
		c.encode(byID[id], element)
		next = append(next, element)
	}

	parent := root
	for _, key := range c.path[:len(c.path)-1] {
		parent = child(parent, key)
	}
	leaf := c.path[len(c.path)-1]
	if len(next) == 0 && parent[leaf] == nil {
		return nil // absent and empty mean the same thing; leave the document alone
	}
	parent[leaf] = next
	return nil
}

func (c listCodec[T]) elements(root cloud.Object) []cloud.Object {
	var node any = root
	for _, key := range c.path {
		obj, ok := node.(cloud.Object)
		if !ok {
			return nil
		}
		node = obj[key]
	}
	list, _ := node.([]any)
	out := make([]cloud.Object, 0, len(list))
	for _, item := range list {
		if obj, ok := item.(cloud.Object); ok {
			out = append(out, obj)
		}
	}
	return out
}

// stableOrder keeps wanted ids that already exist in their current position and appends
// the rest in the order given.
func stableOrder(current []cloud.Object, wanted []string) []string {
	want := make(map[string]bool, len(wanted))
	for _, id := range wanted {
		want[id] = true
	}
	order := make([]string, 0, len(wanted))
	placed := map[string]bool{}
	for _, e := range current {
		if id := asString(e["id"]); want[id] {
			order = append(order, id)
			placed[id] = true
		}
	}
	for _, id := range wanted {
		if !placed[id] {
			order = append(order, id)
		}
	}
	return order
}

func (c listCodec[T]) name() string {
	return strings.Join(c.path, ".")
}
