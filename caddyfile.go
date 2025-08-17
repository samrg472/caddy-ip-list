package caddy_ip_list

import (
	"bufio"
	"context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
	"fmt"
	"net/http"
	"net/netip"
    "os"
    "path/filepath"
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
	// Number of retries for fetching the IP list. Default is 0 (no retries).
	Retries int `json:"retries,omitempty"`

    // Optional path to a cache file. If not set, a file under Caddy's data
    // directory will be used, derived from the URLs.
    CacheFile string `json:"cache_file,omitempty"`

	// Holds the parsed CIDR ranges from Ranges.
	ranges []netip.Prefix

	ctx  caddy.Context
	lock *sync.RWMutex
    log  *zap.Logger
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
	var lastErr error
	for attempt := 0; attempt <= s.Retries; attempt++ {
        ctx, cancel := s.getContext()

        req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
        if err != nil {
            lastErr = err
            cancel()
            break
        }

        resp, err := http.DefaultClient.Do(req)
        if err != nil {
            lastErr = err
            cancel()
        } else {
            if resp.StatusCode < 200 || resp.StatusCode > 299 {
                // drain and close body before next attempt
                _ = resp.Body.Close()
                lastErr = fmt.Errorf("fetch %s returned HTTP %d", api, resp.StatusCode)
                cancel()
            } else {
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
                        _ = resp.Body.Close()
                        cancel()
                        return nil, err
                    }
                    prefixes = append(prefixes, prefix)
                }
                // capture scanner error before closing body
                scanErr := scanner.Err()
                _ = resp.Body.Close()
                cancel()
                if scanErr != nil {
                    lastErr = scanErr
                } else {
                    return prefixes, nil // Success
                }
            }
        }

		// If not last attempt, delay before retrying
		if attempt < s.Retries {
			time.Sleep(1 * time.Second)
		}
	}
	// After all attempts
	return nil, fmt.Errorf("after %d retries: %w", s.Retries, lastErr)
}

type cacheFileContents struct {
    Prefixes  []string  `json:"prefixes"`
    UpdatedAt time.Time `json:"updated_at"`
}

func (s *URLIPRange) cachePath() (string, error) {
    if s.CacheFile != "" {
        return s.CacheFile, nil
    }
    // derive from URLs
    joined := strings.Join(s.URLs, "|")
    sum := sha256.Sum256([]byte(joined))
    name := "ip-list-cache-" + hex.EncodeToString(sum[:]) + ".json"
    dir := caddy.AppDataDir()
    if dir == "" {
        // fallback to current working directory
        dir = "."
    }
    return filepath.Join(dir, name), nil
}

func (s *URLIPRange) loadFromCache() ([]netip.Prefix, error) {
    path, err := s.cachePath()
    if err != nil {
        return nil, err
    }
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    var contents cacheFileContents
    if err := json.NewDecoder(f).Decode(&contents); err != nil {
        return nil, err
    }
    prefixes := make([]netip.Prefix, 0, len(contents.Prefixes))
    for _, p := range contents.Prefixes {
        prefix, err := caddyhttp.CIDRExpressionToPrefix(p)
        if err != nil {
            return nil, fmt.Errorf("invalid prefix in cache %q: %w", p, err)
        }
        prefixes = append(prefixes, prefix)
    }
    return prefixes, nil
}

func (s *URLIPRange) saveToCache(prefixes []netip.Prefix) error {
    path, err := s.cachePath()
    if err != nil {
        return err
    }
    // ensure directory exists
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return err
    }
    // prepare contents
    contents := cacheFileContents{UpdatedAt: time.Now()}
    contents.Prefixes = make([]string, 0, len(prefixes))
    for _, p := range prefixes {
        contents.Prefixes = append(contents.Prefixes, p.String())
    }
    // write atomically
    tmp := path + ".tmp"
    f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
    if err != nil {
        return err
    }
    enc := json.NewEncoder(f)
    enc.SetIndent("", "  ")
    if err := enc.Encode(&contents); err != nil {
        f.Close()
        _ = os.Remove(tmp)
        return err
    }
    if err := f.Close(); err != nil {
        _ = os.Remove(tmp)
        return err
    }
    return os.Rename(tmp, path)
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
    s.log = ctx.Logger()

	// Perform initial fetch
    initialRanges, err := s.getPrefixes()
    if err != nil {
        // Attempt to load from cache so we can start even when sources are down
        cached, cacheErr := s.loadFromCache()
        if cacheErr != nil {
            return fmt.Errorf("failed to fetch initial IP ranges and no cache available: fetch error: %v, cache error: %v", err, cacheErr)
        }
        s.ranges = cached
        if s.log != nil {
            s.log.Warn("using cached IP ranges due to fetch failure on startup", zap.Error(err))
        }
    } else {
        s.ranges = initialRanges
        if err := s.saveToCache(initialRanges); err != nil && s.log != nil {
            s.log.Warn("failed to save IP ranges cache", zap.Error(err))
        }
    }

	// update in background
	go s.refreshLoop()
	return nil
}

func (s *URLIPRange) refreshLoop() {
	if s.Interval == 0 {
		s.Interval = caddy.Duration(time.Hour)
	}

	ticker := time.NewTicker(time.Duration(s.Interval))
	for {
		select {
		case <-ticker.C:
			fullPrefixes, err := s.getPrefixes()
			if err != nil {
                if s.log != nil {
                    s.log.Warn("failed to refresh IP ranges; keeping existing cache", zap.Error(err))
                }
                break
			}

			s.lock.Lock()
			s.ranges = fullPrefixes
			s.lock.Unlock()
            if err := s.saveToCache(fullPrefixes); err != nil && s.log != nil {
                s.log.Warn("failed to save IP ranges cache after refresh", zap.Error(err))
            }
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
		case "retries":
			if !d.NextArg() {
				return d.ArgErr()
			}
			var n int
			_, err := fmt.Sscanf(d.Val(), "%d", &n)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid retries value: %s", d.Val())
			}
			m.Retries = n
        case "cache_file":
            if !d.NextArg() {
                return d.ArgErr()
            }
            m.CacheFile = d.Val()
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
