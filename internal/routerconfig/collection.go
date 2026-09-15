package routerconfig

import (
	"fmt"
	"strings"

	"github.com/TwilightCoders/terraform-provider-alta-labs/internal/cloud"
)

// listCodec maps an ordered list of identified items to a JSON array of objects.
//
// Writes are authoritative: the array becomes exactly the given items, in order. An
// item whose id already exists is overlaid onto that element, so fields the codec does
// not model are kept.
type listCodec[T any] struct {
	// path locates the array from the document root, e.g. firewall → nat → rules.
	path []string
	id   func(T) string
	// decode reads an item from an element.
	decode func(cloud.Object) T
	// encode overlays an item onto an element (fresh or existing).
	encode func(T, cloud.Object)
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
	existing := map[string]cloud.Object{}
	for _, e := range c.elements(root) {
		existing[asString(e["id"])] = e
	}

	seen := map[string]bool{}
	next := make([]any, 0, len(items))
	for _, item := range items {
		id := c.id(item)
		if id == "" {
			return fmt.Errorf("%s: every item needs an id", c.name())
		}
		if seen[id] {
			return fmt.Errorf("%s: duplicate id %q", c.name(), id)
		}
		seen[id] = true

		element, ok := existing[id]
		if !ok {
			element = cloud.Object{}
		}
		c.encode(item, element)
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

func (c listCodec[T]) name() string {
	return strings.Join(c.path, ".")
}
