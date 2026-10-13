package platform

import (
	"errors"
	"net/url"
	"strings"
)

// WebURL restricts model-produced links to browser web pages. Links never
// become shell commands, local files or application protocol handlers.
func WebURL(address string) (*url.URL, error) {
	target, err := url.Parse(strings.TrimSpace(address))
	if err != nil || target.Hostname() == "" || target.User != nil ||
		!strings.EqualFold(target.Scheme, "https") && !strings.EqualFold(target.Scheme, "http") {
		return nil, errors.New("informe um endereço de página HTTP ou HTTPS válido")
	}
	target.Scheme = strings.ToLower(target.Scheme)
	return target, nil
}
