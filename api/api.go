// Package api holds the observed description of the Alta Labs management API.
//
// Alta publishes nothing, so schema.json is evidence: endpoints extracted from the
// portal bundle recorded in its provenance, object shapes fitted from captured
// responses, and a compile mapping recording how cloud fields reach a router, including
// where the cloud's own compiler drops them. Regenerate the fitted parts with
// `make schema`; `make portal-endpoints` re-extracts the endpoint list.
package api

import _ "embed"

//go:embed schema.json
var Schema []byte
