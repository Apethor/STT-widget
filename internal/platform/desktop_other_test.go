//go:build !windows

package platform

import (
	"errors"
	"testing"
)

func TestDesktopOperationsReportUnsupported(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		call func() error
	}{
		{name: "pin", call: func() error { return SetPinned(true) }},
		{name: "unpin", call: func() error { return SetPinned(false) }},
		{name: "activate", call: Activate},
		{name: "dark title bar", call: func() error { return DarkTitlebar(true) }},
		{name: "copy", call: func() error { return Copy("unchanged clipboard") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("desktop operation = %v, want ErrUnsupported", err)
			}
		})
	}
}
