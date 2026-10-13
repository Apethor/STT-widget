//go:build !windows

package platform

func OpenURL(address string) error {
	if _, err := WebURL(address); err != nil {
		return err
	}
	return ErrUnsupported
}
