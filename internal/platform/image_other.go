//go:build !windows

package platform

func OpenImage(path string) error {
	if _, err := imagePath(path); err != nil {
		return err
	}
	return ErrUnsupported
}
