// Package redis wraps the maintained github.com/redis/go-redis client with the
// small surface gorouter needs for its multi-instance features (shared
// response cache and shared health probes). Every key is namespaced under the
// client's prefix so gorouter never collides with other applications sharing
// the same Redis instance.
//
// Fail-open by design: callers treat errors as "not present / no-op", so a
// Redis outage never breaks the request path — the in-memory layer keeps
// serving. Configure with redis://[:password@]host:port[/db].
package redis

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultPrefix namespaces every key written by this client.
const DefaultPrefix = "gorouter"

// DefaultTimeout bounds each command's round-trip.
const DefaultTimeout = 500 * time.Millisecond

// Client is a thin wrapper over go-redis applying the key prefix and a default
// timeout. A nil *Client (from a failed New) disables Redis entirely.
type Client struct {
	r       *redis.Client
	prefix  string
	timeout time.Duration
}

// New parses a redis:// URL and returns a client. Parse errors are fatal;
// connectivity is only tested on first use.
func New(rawURL string) (*Client, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url: %w", err)
	}
	return &Client{r: redis.NewClient(opts), prefix: DefaultPrefix, timeout: DefaultTimeout}, nil
}

// Addr returns the configured server address (for logs).
func (c *Client) Addr() string { return c.r.Options().Addr }

// Close closes the underlying connection pool.
func (c *Client) Close() error { return c.r.Close() }

// Ping verifies connectivity. Used at startup to decide whether to enable
// multi-instance features.
func (c *Client) Ping(ctx context.Context) error {
	return c.r.Ping(ctx).Err()
}

// Set stores value under key with the given TTL (ignored when <= 0).
func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return c.withTimeout(ctx, func(ctx context.Context) error { return c.r.Set(ctx, c.key(key), value, 0).Err() })
	}
	return c.withTimeout(ctx, func(ctx context.Context) error { return c.r.Set(ctx, c.key(key), value, ttl).Err() })
}

// SetNX stores value only if key does not exist. Returns true when the value
// was set (the caller holds the lock/reservation).
func (c *Client) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	var ok bool
	err := c.withTimeout(ctx, func(ctx context.Context) error {
		var err error
		ok, err = c.r.SetNX(ctx, c.key(key), value, ttl).Result()
		return err
	})
	return ok, err
}

// Get returns the value for key, or (nil, nil) when absent.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	var b []byte
	err := c.withTimeout(ctx, func(ctx context.Context) error {
		var err error
		b, err = c.r.Get(ctx, c.key(key)).Bytes()
		if err == redis.Nil {
			return nil // not found
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Del removes the given keys.
func (c *Client) Del(ctx context.Context, keys ...string) error {
	return c.withTimeout(ctx, func(ctx context.Context) error {
		return c.r.Del(ctx, c.prefixed(keys)...).Err()
	})
}

// Exists reports whether the key is present.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	var n int64
	err := c.withTimeout(ctx, func(ctx context.Context) error {
		var err error
		n, err = c.r.Exists(ctx, c.key(key)).Result()
		return err
	})
	return n > 0, err
}

// ScanKeys returns every key matching the glob pattern, with the client's
// prefix stripped from the results. Iterates the whole keyspace via SCAN.
func (c *Client) ScanKeys(ctx context.Context, pattern string) ([]string, error) {
	prefixed := c.prefix + ":" + pattern
	var out []string
	iter := c.r.Scan(ctx, 0, prefixed, 200).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		if s, ok := strings.CutPrefix(key, c.prefix+":"); ok {
			out = append(out, s)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// key applies the namespace prefix.
func (c *Client) key(k string) string { return c.prefix + ":" + k }

func (c *Client) prefixed(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = c.key(k)
	}
	return out
}

// withTimeout applies the default per-command timeout via context.
func (c *Client) withTimeout(ctx context.Context, fn func(context.Context) error) error {
	if c.timeout <= 0 {
		return fn(ctx)
	}
	dctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return fn(dctx)
}
