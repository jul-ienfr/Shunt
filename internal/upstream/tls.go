package upstream

import (
	"crypto/tls"
	"fmt"
)

// TLSConfig builds a verifying TLS configuration for a profile host. There is
// deliberately no insecure mode and no environment override.
func TLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: serverName,
		NextProtos: []string{"http/1.1"},
	}
}

// ValidateTLSHost rejects an empty TLS server name so SNI is always explicit.
func ValidateTLSHost(host string) error {
	if host == "" {
		return fmt.Errorf("empty TLS host")
	}
	return nil
}
