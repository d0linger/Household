package auth

import "testing"

func TestDeviceLabelIsCoarse(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36":           "Chrome · Windows",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0": "Edge · Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_1) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/19.0 Safari/605.1.15":        "Safari · macOS",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 19_0 like Mac OS X) AppleWebKit/605.1.15 Version/19.0 Mobile/15E148 Safari/604.1":       "Safari · iOS",
		"Mozilla/5.0 (Android 16; Mobile; rv:140.0) Gecko/140.0 Firefox/140.0":                                                      "Firefox · Android",
		"Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0":                                                    "Firefox · Linux",
		"curl/8.0": "Other browser · Other system",
		"":         "Other browser · Other system",
	} {
		if got := DeviceLabel(ua); got != want {
			t.Fatalf("DeviceLabel(%q) = %q, want %q", ua, got, want)
		}
	}
	if DeviceLabel("Mozilla/5.0 (Windows NT 10.0) Chrome/141.0") != DeviceLabel("Mozilla/5.0 (Windows NT 10.0) Chrome/142.0") {
		t.Fatal("a browser update changed the label")
	}
}
