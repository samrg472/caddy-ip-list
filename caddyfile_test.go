package caddy_ip_list

import (
	"context"
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
