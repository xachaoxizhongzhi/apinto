package thrift

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
)

func config(files map[string]string) *Config {
	raw, _ := json.Marshal(files)
	sum := sha256.Sum256(raw)
	return &Config{Revision: hex.EncodeToString(sum[:]), Files: files}
}
func TestImmutableResourceLifecycle(t *testing.T) {
	cfg := config(map[string]string{"idl/api.thrift": `struct Req{} struct Res{} service Demo {Res Get(1:Req req)(api.get="/get") Res List(1:Req req)(api.get="/list")}`})
	name := "thrift-" + cfg.Revision
	resource, err := Create(name+"@transcode", name, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := resource.(*Worker)
	defer w.Stop()
	cli, _, release, err := w.Acquire("idl/api.thrift", "Demo", "backend", "Get")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, _, done, err := w.Acquire("idl/api.thrift", "Demo", "backend", "List")
			if err != nil {
				t.Error(err)
				return
			}
			defer done()
			if next != cli {
				t.Error("client was duplicated per method")
			}
		}()
	}
	wg.Wait()
	other := config(map[string]string{"idl/api.thrift": cfg.Files["idl/api.thrift"] + " // changed"})
	if err = w.Reset(other, nil); err == nil {
		t.Fatal("immutable resource overwritten")
	}
	if err = w.Reset(cfg, nil); err != nil {
		t.Fatal("idempotent preparation failed", err)
	}
	if err = w.Stop(); err != nil {
		t.Fatal(err)
	}
	if len(w.clients) != 1 {
		t.Fatal("active lease closed during stop")
	}
	if _, _, _, err = w.Acquire("idl/api.thrift", "Demo", "backend", "Get"); err == nil {
		t.Fatal("stopped resource accepted a new lease")
	}
	if err = release(); err != nil {
		t.Fatal(err)
	}
	if len(w.clients) != 0 {
		t.Fatal("last request did not release client")
	}
	release()
}
func TestInvalidContractResources(t *testing.T) {
	for _, files := range []map[string]string{
		{"../escape.thrift": "struct A{}"},
		{"a.txt": "struct A{}"},
		{"a.thrift": "include \"missing.thrift\""},
		{"a.thrift": "include \"b.thrift\"", "b.thrift": "include \"a.thrift\""},
	} {
		cfg := config(files)
		if _, err := Create("x", "thrift-"+cfg.Revision, cfg, nil); err == nil {
			t.Fatal("invalid bundle accepted")
		}
	}
	cfg := config(map[string]string{"a.thrift": "struct A{}"})
	cfg.Revision = "bad"
	if _, err := Create("x", "thrift-bad", cfg, nil); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}
