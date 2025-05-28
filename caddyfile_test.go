package caddy_ip_list

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

func TestDefault(t *testing.T) {
	testDefault(t, `url`)
	testDefault(t, `list { }`)
}

func testDefault(t *testing.T, input string) {
	d := caddyfile.NewTestDispenser(input)

	r := URLIPRange{}
	err := r.UnmarshalCaddyfile(d)
	if err != nil {
		t.Errorf("unmarshal error for %q: %v", input, err)
	}

	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	defer cancel()

	err = r.Provision(ctx)
	if err != nil {
		t.Errorf("error provisioning %q: %v", input, err)
	}
}

func TestUnmarshal(t *testing.T) {
	input := `
	list {
	    url https://www.cloudflare.com/ips-v4
		interval 1.5h
		timeout 30s
	}`

	d := caddyfile.NewTestDispenser(input)

	r := URLIPRange{}
	err := r.UnmarshalCaddyfile(d)
	if err != nil {
		t.Errorf("unmarshal error: %v", err)
	}

	expectedInterval := caddy.Duration(90 * time.Minute)
	if expectedInterval != r.Interval {
		t.Errorf("incorrect interval: expected %v, got %v", expectedInterval, r.Interval)
	}

	expectedTimeout := caddy.Duration(30 * time.Second)
	if expectedTimeout != r.Timeout {
		t.Errorf("incorrect timeout: expected %v, got %v", expectedTimeout, r.Timeout)
	}
}

// Simulates being nested in another block.
func TestUnmarshalNested(t *testing.T) {
	input := `{
				list {
				    url https://www.cloudflare.com/ips-v4
					interval 1.5h
					timeout 30s
				}
				other_module 10h
			}`

	d := caddyfile.NewTestDispenser(input)

	// Enter the outer block.
	d.Next()
	d.NextBlock(d.Nesting())

	r := URLIPRange{}
	err := r.UnmarshalCaddyfile(d)
	if err != nil {
		t.Errorf("unmarshal error: %v", err)
	}

	expectedInterval := caddy.Duration(90 * time.Minute)
	if expectedInterval != r.Interval {
		t.Errorf("incorrect interval: expected %v, got %v", expectedInterval, r.Interval)
	}

	expectedTimeout := caddy.Duration(30 * time.Second)
	if expectedTimeout != r.Timeout {
		t.Errorf("incorrect timeout: expected %v, got %v", expectedTimeout, r.Timeout)
	}

	d.Next()
	if d.Val() != "other_module" {
		t.Errorf("cursor at unexpected position, expected 'other_module', got %v", d.Val())
	}
}

// TestRetriesProvision tests that retries work for provision.
func TestRetriesProvision(t *testing.T) {
	var failCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cnt := atomic.AddInt32(&failCount, 1)
		if cnt <= 2 {
			http.Error(w, "fail", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("192.0.2.1/32\n"))
	}))
	defer server.Close()

	input := `
	list {
	    url ` + server.URL + `
	    retries 2
	}`

	d := caddyfile.NewTestDispenser(input)
	r := URLIPRange{}
	err := r.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	defer cancel()
	err = r.Provision(ctx)
	if err != nil {
		t.Fatalf("Provision failed after retries: %v", err)
	}
	if n := atomic.LoadInt32(&failCount); n != 3 {
		t.Errorf("expected 3 calls to backend (2 fails + 1 success), got %d", n)
	}
}

// TestRetriesProvisionAllFail tests that provision fails after all retries fail.
func TestRetriesProvisionAllFail(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "fail", http.StatusInternalServerError)
	}))
	defer server.Close()

	input := `
	list {
	    url ` + server.URL + `
	    retries 2
	}`

	d := caddyfile.NewTestDispenser(input)
	r := URLIPRange{}
	err := r.UnmarshalCaddyfile(d)
	if err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	ctx, cancel := caddy.NewContext(caddy.Context{Context: context.Background()})
	defer cancel()
	err = r.Provision(ctx)
	if err == nil {
		t.Errorf("Provision should have failed, but returned nil")
	}
	if hits := atomic.LoadInt32(&hits); hits != 3 {
		t.Errorf("expected 3 attempts, got %d", hits)
	}
}
