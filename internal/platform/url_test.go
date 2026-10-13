package platform

import "testing"

func TestWebURLAcceptsPagesAndRejectsOtherHandlers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		address string
		valid   bool
	}{
		{"https query", "https://example.com/search?q=ol%C3%A1&lang=pt#item", true},
		{"http", "http://example.com/path", true},
		{"uppercase", "HTTPS://example.com/path", true},
		{"relative", "/path", false},
		{"missing host", "https:///path", false},
		{"local file", "file:///C:/Windows/system32/cmd.exe", false},
		{"javascript", "javascript:alert(1)", false},
		{"application protocol", "ms-settings:privacy", false},
		{"credentials", "https://user:password@example.com/", false},
		{"invalid host", "https://bad host/path", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, err := WebURL(tc.address)
			if (err == nil) != tc.valid {
				t.Fatalf("WebURL(%q) retornou %v, %v", tc.address, target, err)
			}
		})
	}
}
