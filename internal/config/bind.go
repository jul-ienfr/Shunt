package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// InterfaceAddress is an address assigned to a local network interface.
type InterfaceAddress struct {
	Name     string
	Address  string
	Loopback bool
	Private  bool
}

// LocalAddresses lists the unicast addresses assigned to local interfaces.
// Wildcard and unspecified addresses are never returned.
func LocalAddresses() ([]InterfaceAddress, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []InterfaceAddress
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			item := InterfaceAddress{
				Name:     iface.Name,
				Address:  ip.String(),
				Loopback: ip.IsLoopback(),
				Private:  IsPrivateIP(ip),
			}
			out = append(out, item)
		}
	}
	return out, nil
}

// IsPrivateIP reports whether ip belongs to a range that may be bound without
// exposing the relay to the public Internet.
func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 10:
			return true
		case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
			return true
		case v4[0] == 192 && v4[1] == 168:
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127:
			return true
		case v4[0] == 169 && v4[1] == 254:
			return true
		}
		return false
	}
	if len(ip) == net.IPv6len {
		// IPv6 unique local addresses (fc00::/7).
		if ip[0]&0xfe == 0xfc {
			return true
		}
		// IPv6 link-local (fe80::/10).
		if ip[0] == 0xfe && ip[1]&0xc0 == 0x80 {
			return true
		}
	}
	return false
}

// IsLoopbackIP reports whether ip is a loopback address.
func IsLoopbackIP(ip net.IP) bool {
	return ip != nil && ip.IsLoopback()
}

// SplitListen splits a host:port listen string, accepting bracketed IPv6.
func SplitListen(listen string) (net.IP, int, error) {
	host, portText, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		return nil, 0, fmt.Errorf("listen must be host:port: %w", err)
	}
	zone := ""
	if i := strings.LastIndex(host, "%"); i >= 0 {
		host, zone = host[:i], host[i+1:]
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, 0, fmt.Errorf("listen host %q must be an IP address", host)
	}
	if zone != "" {
		if ip.To4() != nil {
			return nil, 0, fmt.Errorf("an IPv4 listen address cannot have a zone")
		}
		if iface, err := net.InterfaceByName(zone); err != nil || iface == nil {
			return nil, 0, fmt.Errorf("listen zone %q does not name a local interface", zone)
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, 0, fmt.Errorf("listen port must be numeric")
	}
	if port < 1024 || port > 65535 {
		return nil, 0, fmt.Errorf("listen port must be 1024..65535")
	}
	return ip, port, nil
}

// ValidateListen enforces the bind matrix: loopback is always allowed, a
// specific private address assigned to a local interface requires
// allow_non_loopback, and wildcard or public addresses are rejected.
func ValidateListen(listen string, allowNonLoopback bool) error {
	ip, port, err := SplitListen(listen)
	if err != nil {
		return err
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("listen %s is a wildcard address; wildcard binds are not allowed", listen)
	}
	if ip.IsLoopback() {
		return nil
	}
	if !IsPrivateIP(ip) {
		return fmt.Errorf("listen %s is not a loopback or private local address", listen)
	}
	if !allowNonLoopback {
		return fmt.Errorf("listen %s is non-loopback; set relay.allow_non_loopback = true after confirming the exposure", listen)
	}
	assigned, err := addressAssigned(ip)
	if err != nil {
		return fmt.Errorf("list local interfaces: %w", err)
	}
	if !assigned {
		return fmt.Errorf("listen %s is not assigned to a local interface", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	}
	return nil
}

func addressAssigned(ip net.IP) (bool, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var candidate net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				candidate = v.IP
			case *net.IPAddr:
				candidate = v.IP
			}
			if candidate != nil && candidate.Equal(ip) {
				return true, nil
			}
		}
	}
	return false, nil
}

// NormalizeListen returns the canonical host:port form, bracketing IPv6.
func NormalizeListen(listen string) (string, error) {
	ip, port, err := SplitListen(listen)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
}

// ListenOrigin returns the http:// origin for a listen address, bracketing IPv6.
func ListenOrigin(listen string) string {
	ip, port, err := SplitListen(listen)
	if err != nil {
		return "http://" + listen
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(port))
}
