package client

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

const maxAllowedIPs = 100

var allowedIPsCarrierGradeNAT = netip.MustParsePrefix("100.64.0.0/10")

// NormalizeAllowedIPs mirrors the Management API's address and range rules,
// returning an owned, sorted set. Empty input becomes an explicit empty list.
// Like the backend, exclusions classify the masked network's base address.
func NormalizeAllowedIPs(entries []string) ([]string, error) {
	invalid := func(format string, args ...any) ([]string, error) {
		return nil, &localValidationError{message: fmt.Sprintf(format, args...)}
	}
	if len(entries) > maxAllowedIPs {
		return invalid("allowed_ips can hold at most %d entries", maxAllowedIPs)
	}
	out := make([]string, 0, len(entries))
	for index, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return invalid("allowed_ips entry %d cannot be empty", index+1)
		}
		prefix, err := parseAllowedIP(entry)
		if err != nil {
			return invalid("allowed_ips entry %d is not an IP address or CIDR range", index+1)
		}
		if reason := refusedAllowedIP(prefix); reason != "" {
			return invalid("allowed_ips entry %d %s", index+1, reason)
		}
		if prefix.IsSingleIP() {
			out = append(out, prefix.Addr().String())
		} else {
			out = append(out, prefix.String())
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func parseAllowedIP(entry string) (netip.Prefix, error) {
	if strings.Contains(entry, "/") {
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			return netip.Prefix{}, err
		}
		addr, bits := prefix.Addr(), prefix.Bits()
		if addr.Is4In6() {
			if bits < 96 {
				return netip.Prefix{}, fmt.Errorf("mapped prefix shorter than /96")
			}
			addr, bits = addr.Unmap(), bits-96
		}
		return netip.PrefixFrom(addr, bits).Masked(), nil
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil || addr.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("invalid or zoned address")
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func refusedAllowedIP(prefix netip.Prefix) string {
	addr := prefix.Addr()
	if addr.Is4() && prefix.Bits() < 8 || addr.Is6() && prefix.Bits() < 16 {
		return "is too wide: use a range of /8 or narrower for IPv4, /16 or narrower for IPv6"
	}
	switch {
	case addr.IsUnspecified():
		return "is the unspecified address"
	case addr.IsLoopback():
		return "is a loopback address"
	case addr.IsPrivate():
		return "is a private address: list the public address your requests leave from"
	case addr.IsLinkLocalUnicast(), addr.IsLinkLocalMulticast():
		return "is a link-local address"
	case addr.IsMulticast():
		return "is a multicast address"
	}
	if addr.Is4() && allowedIPsCarrierGradeNAT.Contains(addr) {
		return "is a shared (carrier-grade NAT) address: list the public address your requests leave from"
	}
	return ""
}
