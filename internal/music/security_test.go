package music

import (
	"net"
	"testing"
)

func TestPublicIPRejectsMetadataAndSpecialNetworks(t *testing.T) {
	for _, address := range []string{"100.100.100.200", "100.64.0.1", "100.127.255.254", "::ffff:100.100.100.200", "169.254.169.254", "127.0.0.1", "10.0.0.1", "192.0.2.1", "198.18.0.1", "224.0.0.1", "::1", "fc00::1", "fe80::1", "64:ff9b::6464:64c8", "2002:6464:64c8::1", "2001:db8::1"} {
		if IsPublicIP(net.ParseIP(address)) {
			t.Errorf("accepted reserved address %s", address)
		}
	}
	for _, address := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !IsPublicIP(net.ParseIP(address)) {
			t.Errorf("rejected public address %s", address)
		}
	}
}
