package http_to_kitex

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bytedance/gopkg/cloud/metainfo"
	"golang.org/x/net/http/httpguts"
)

// Copy the mapping so later edits to a Config cannot mutate an active converter.
func validateHeaderMapping(config map[string]string) (map[string]string, error) {
	mapping := make(map[string]string, len(config))
	targets := make(map[string]bool, len(config))
	for header, key := range config {
		if !httpguts.ValidHeaderFieldName(header) || !httpguts.ValidHeaderFieldName(key) {
			return nil, fmt.Errorf("header_mapping requires nonempty HTTP header names and token-form metadata keys")
		}
		canonical := http.CanonicalHeaderKey(header)
		if _, exists := mapping[canonical]; exists {
			return nil, fmt.Errorf("header_mapping contains duplicate header %s", canonical)
		}
		if targets[key] {
			return nil, fmt.Errorf("header_mapping contains duplicate metadata key %s", key)
		}
		mapping[canonical] = key
		targets[key] = true
	}
	return mapping, nil
}

// Read the final proxy headers after route plugins. Only explicitly mapped HTTP
// headers enter persistent Kitex metadata; no identity or value validation occurs.
func (c *Complete) rpcContext(ctx context.Context, headers http.Header) context.Context {
	for header, key := range c.headerMapping {
		value := headers.Get(header)
		// metainfo does not encode empty values. Clear inherited values so a missing
		// or empty header cannot reuse stale identity from the parent context.
		if value == "" {
			ctx = metainfo.DelPersistentValue(ctx, key)
		} else {
			ctx = metainfo.WithPersistentValue(ctx, key, value)
		}
	}
	return ctx
}
