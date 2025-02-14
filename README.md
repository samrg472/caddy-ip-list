# Caddy-IP-List Module

This module retrieves an IP list from specified URLs at a defined interval. It is designed to integrate with other modules like `dynamic_client_ip` or `trusted_proxy` in Caddy.

Supported from Caddy v2.6.3 onwards.

## Example Configuration

### Using `dynamic_client_ip`

You can get `dynamic_client_ip` from [here](https://github.com/tuzzmaniandevil/caddy-dynamic-clientip)

```caddy
@denied dynamic_client_ip list {
    url https://www.cloudflare.com/ips-v4  # specify the URL to fetch the IP list
    url https://www.cloudflare.com/ips-v6  # You can use multiple URLs
    interval 12h
    timeout 15s
}
abort @denied
```

### Using `trusted_proxy`

```caddy
trusted_proxies list {
    url https://www.cloudflare.com/ips-v4  # specify the URL to fetch the IP list
    url https://www.cloudflare.com/ips-v6  # You can use multiple URLs
    interval 12h
    timeout 15s
}
```

## Defaults

| Name     | Description                                      | Type     | Default    |
| -------- | ------------------------------------------------ | -------- | ---------- |
| url      | URL(s) to retrieve the IP list                   | string   | *required* |
| interval | Frequency at which the IP list is retrieved      | duration | 1h         |
| timeout  | Maximum time to wait for a response from the URL | duration | no timeout |
