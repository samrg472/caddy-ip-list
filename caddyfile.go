package caddy_ip_list

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule(URLIPRange{})
}

// URLIPRange provides a range of IP address prefixes (CIDRs) retrieved from url.
type URLIPRange struct {
	// List of URLs to fetch the IP ranges from.
	URLs []string `json:"url"`
	// refresh Interval
	Interval caddy.Duration `json:"interval,omitempty"`
	// request Timeout
	Timeout caddy.Duration `json:"timeout,omitempty"`

	// Holds the parsed CIDR ranges from Ranges.
	ranges []netip.Prefix

	ctx  caddy.Context
	lock *sync.RWMutex
}

// CaddyModule returns the Caddy module information.
func (URLIPRange) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.ip_sources.list",
		New: func() caddy.Module { return new(URLIPRange) },
	}
}

// getContext returns a cancelable context, with a timeout if configured.
func (s *URLIPRange) getContext() (context.Context, context.CancelFunc) {
	if s.Timeout > 0 {
		return context.WithTimeout(s.ctx, time.Duration(s.Timeout))
	}
	return context.WithCancel(s.ctx)
}

func (s *URLIPRange) fetch(api string) ([]netip.Prefix, error) {
	ctx, cancel := s.getContext()
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	var prefixes []netip.Prefix
	for scanner.Scan() {
		line := scanner.Text()

		// Remove comments from the line
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}

		// Trim spaces
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Convert to prefix
		prefix, err := caddyhttp.CIDRExpressionToPrefix(line)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes, nil
}

func (s *URLIPRange) getPrefixes() ([]netip.Prefix, error) {
	var fullPrefixes []netip.Prefix
	for _, url := range s.URLs {
		// Fetch list
		prefixes, err := s.fetch(url)
		if err != nil {
			return nil, err
		}
		fullPrefixes = append(fullPrefixes, prefixes...)
	}

	return fullPrefixes, nil
}

func (s *URLIPRange) Provision(ctx caddy.Context) error {
	s.ctx = ctx
	s.lock = new(sync.RWMutex)

	// Perform initial fetch
	initialPrefixes, err := s.getPrefixes()
	if err != nil {
		// Wrap the error for more context when Caddy reports it
		return fmt.Errorf("failed to perform initial fetch of IP ranges: %w", err)
	}
	// Store the successfully fetched ranges. No lock needed here as refreshLoop hasn't started.
	s.ranges = initialPrefixes

	// update in background
	go s.refreshLoop()
	return nil
}

func (s *URLIPRange) refreshLoop() {
	if s.Interval == 0 {
		s.Interval = caddy.Duration(time.Hour) // Default interval
	}

	ticker := time.NewTicker(time.Duration(s.Interval))
	// Initial fetch is now done in Provision, so we remove it from here.
	// The loop will start, and the first refresh will happen after the first Interval.

	for {
		select {
		case <-ticker.C:
			fullPrefixes, err := s.getPrefixes()
			if err != nil {
				// Log the error for background refreshes. Caddy continues with the old list.
				// Ensure logger is available before attempting to use it.
				// s.ctx is guaranteed to be non-nil by Provision.
				if logger := s.ctx.Logger(); logger != nil {
					logger.Error("failed to refresh IP ranges from URLs",
						zap.Strings("urls", s.URLs),
						zap.Error(err))
				}
				// Continue to the next tick, keeping the old ranges.
				break
			}

			s.lock.Lock()
			s.ranges = fullPrefixes
			s.lock.Unlock()
		case <-s.ctx.Done():
			ticker.Stop()
			return
		}
	}
}

func (s *URLIPRange) GetIPRanges(_ *http.Request) []netip.Prefix {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.ranges
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler.
//
//	list {
//	   interval val
//	   timeout val
//	   url string
//	}
func (m *URLIPRange) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	d.Next() // Skip module name.

	// No same-line options are supported
	if d.NextArg() {
		return d.ArgErr()
	}

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "interval":
			if !d.NextArg() {
				return d.ArgErr()
			}
			val, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return err
			}
			m.Interval = caddy.Duration(val)
		case "timeout":
			if !d.NextArg() {
				return d.ArgErr()
			}
			val, err := caddy.ParseDuration(d.Val())
			if err != nil {
				return err
			}
			m.Timeout = caddy.Duration(val)
		case "url":
			if !d.NextArg() {
				return d.ArgErr()
			}
			m.URLs = append(m.URLs, d.Val())
		default:
			return d.ArgErr()
		}
	}

	return nil
}

// Interface guards
var (
	_ caddy.Module            = (*URLIPRange)(nil)
	_ caddy.Provisioner       = (*URLIPRange)(nil)
	_ caddyfile.Unmarshaler   = (*URLIPRange)(nil)
	_ caddyhttp.IPRangeSource = (*URLIPRange)(nil)
)
