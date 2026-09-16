// Package apischema describes the Alta Labs management API as observed.
//
// Alta publishes no API schema, so this one is evidence rather than contract: object
// shapes are fitted from captured responses (see Fit), endpoints are extracted from the
// portal bundle, and both are re-checked by tests. Anything here can be wrong the moment
// Alta changes something, which is exactly what the conformance tests are for.
package apischema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Schema is the observed API surface.
type Schema struct {
	Provenance  Provenance        `json:"provenance"`
	Endpoints   []Endpoint        `json:"endpoints"`
	Objects     map[string]Object `json:"objects"`
	Collections []Collection      `json:"collections"`
	Compile     []CompileRule     `json:"compile"`
}

// Provenance records what the description was derived from.
type Provenance struct {
	Captured       string `json:"captured"`
	RouterFirmware string `json:"router_firmware"`
	PortalBundle   Bundle `json:"portal_bundle"`
	Note           string `json:"note"`
}

// Bundle identifies the portal JavaScript the endpoints were extracted from.
type Bundle struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Endpoint is one API call the provider relies on.
type Endpoint struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	// Token says where the Cognito id token goes: "header" or "body".
	Token     string `json:"token"`
	Semantics string `json:"semantics"`
}

// Object is a JSON object shape, fitted from captures.
type Object struct {
	// Source locates instances in a capture, e.g. "site:firewall.nat.rules[]".
	Source string `json:"source"`
	// Unobserved marks an object never yet seen in a capture: its fields are declared
	// from another source and the fitter leaves them alone. If instances do turn up,
	// fitting fails, which is the prompt to fit them properly.
	Unobserved bool `json:"unobserved,omitempty"`
	// PartialCapture marks an object whose checked-in captures are reduced (fields the
	// tests do not need are stripped when captures are anonymised). Fitting augments the
	// declared fields instead of replacing them, so evidence gathered elsewhere survives.
	PartialCapture bool             `json:"partial_capture,omitempty"`
	Fields         map[string]Field `json:"fields"`
}

// Field is one observed field.
type Field struct {
	Type     string `json:"type"`
	Optional bool   `json:"optional,omitempty"`
	// Repr notes a representation quirk, e.g. a port seen as number and string.
	Repr string `json:"repr,omitempty"`
	// Values lists the closed vocabulary observed for this field, where it has one.
	Values []string `json:"values,omitempty"`
}

// Collection describes how a list of objects behaves when written.
type Collection struct {
	Name   string `json:"name"`
	Object string `json:"object"`
	// Key is the field identifying an element, empty for keyed maps.
	Key string `json:"key,omitempty"`
	// Ordered marks collections whose order changes behaviour.
	Ordered bool `json:"ordered"`
	// Authoritative marks collections the provider rewrites wholesale.
	Authoritative bool   `json:"authoritative"`
	CloudPath     string `json:"cloud_path"`
}

// CompileRule records how a cloud field reaches the device, including where the cloud's
// own compiler is known to drop or ignore it.
type CompileRule struct {
	Cloud  string `json:"cloud"`
	Device string `json:"device"`
	// Status is "ok" or "defect".
	Status   string `json:"status"`
	Note     string `json:"note,omitempty"`
	Observed string `json:"observed,omitempty"`
}

// Load parses a schema document.
func Load(data []byte) (*Schema, error) {
	var s Schema
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("apischema: %w", err)
	}
	return &s, nil
}

// Marshal renders the schema the way the checked-in document is formatted.
func (s *Schema) Marshal() ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Defects returns the compile rules where the cloud does not deliver what it stores.
func (s *Schema) Defects() []CompileRule {
	var out []CompileRule
	for _, r := range s.Compile {
		if r.Status == "defect" {
			out = append(out, r)
		}
	}
	return out
}

// objectNames returns object names in a stable order.
func (s *Schema) objectNames() []string {
	names := make([]string, 0, len(s.Objects))
	for name := range s.Objects {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
