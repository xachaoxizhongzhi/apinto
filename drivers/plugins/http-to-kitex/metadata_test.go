package http_to_kitex

import (
	"context"
	"net/http"
	"testing"

	"github.com/bytedance/gopkg/cloud/metainfo"
)

func TestHeaderMapping(t *testing.T) {
	config := map[string]string{"x-user-id": "identity", "x-device-id": "device", "x-empty": "empty", "x-missing": "missing"}
	mapping, err := validateHeaderMapping(config)
	if err != nil {
		t.Fatal(err)
	}
	config["x-user-id"] = "changed"
	c := &Complete{headerMapping: mapping}
	parent := metainfo.WithPersistentValues(context.Background(), "identity", "old", "empty", "old", "missing", "old", "trace", "keep")
	headers := http.Header{}
	headers.Set("X-User-ID", "arbitrary value")
	headers.Set("X-Device-Id", "device-123")
	headers.Set("X-Empty", "")
	headers.Set("Authorization", "not mapped")
	ctx := c.rpcContext(parent, headers)
	for key, want := range map[string]string{"identity": "arbitrary value", "device": "device-123", "empty": "", "missing": "", "trace": "keep", "Authorization": "", "changed": ""} {
		if got, _ := metainfo.GetPersistentValue(ctx, key); got != want {
			t.Fatalf("%s: got %q want %q", key, got, want)
		}
	}
	if got, _ := metainfo.GetPersistentValue(parent, "identity"); got != "old" {
		t.Fatal("parent mutated")
	}
	next := c.rpcContext(parent, http.Header{})
	if got, _ := metainfo.GetPersistentValue(next, "identity"); got != "" {
		t.Fatal("identity leaked between requests")
	}
}

func TestInvalidHeaderMapping(t *testing.T) {
	for _, config := range []map[string]string{
		{"": "id"}, {"X-Id": ""}, {"bad header": "id"}, {"X-Id": "bad\nkey"},
		{"X-Id": "id", "x-id": "other"}, {"X-A": "id", "X-B": "id"},
	} {
		if _, err := validateHeaderMapping(config); err == nil {
			t.Fatalf("accepted invalid mapping: %#v", config)
		}
	}
}
