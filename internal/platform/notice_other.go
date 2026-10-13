//go:build !windows

package platform

import "fmt"

func ShowNotice(title, message string) error {
	return fmt.Errorf("%s: %s", title, message)
}
