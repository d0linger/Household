package main

import "testing"

func TestHealthURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, listen, want string
	}{
		{"default", "", "http://127.0.0.1:8080/health/ready"},
		{"all interfaces", ":9100", "http://127.0.0.1:9100/health/ready"},
		{"unspecified IPv4", "0.0.0.0:9100", "http://127.0.0.1:9100/health/ready"},
		{"unspecified IPv6", "[::]:9100", "http://127.0.0.1:9100/health/ready"},
		{"specific IPv6", "[::1]:9100", "http://[::1]:9100/health/ready"},
		{"specific host", "localhost:9100", "http://localhost:9100/health/ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := healthURL(tc.listen)
			if err != nil || got != tc.want {
				t.Fatalf("healthURL(%q) = %q, %v; want %q", tc.listen, got, err, tc.want)
			}
		})
	}
	for _, listen := range []string{"8080", "localhost:", ":"} {
		if _, err := healthURL(listen); err == nil {
			t.Fatalf("healthURL(%q) accepted an address without a port", listen)
		}
	}
}
