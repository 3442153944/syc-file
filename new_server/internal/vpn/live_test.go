package vpn

import "testing"

func TestParseWgDump(t *testing.T) {
	dump := "privkey\tpubkey\t51820\toff\n" +
		"pubLAN\t(none)\t192.168.31.10:51820\t172.1.0.2/32\t1792470000\t123\t456\toff\n" +
		"pubRelay\t(none)\t127.0.0.1:55122\t172.1.0.3/32\t1792470000\t1\t2\toff\n" +
		"pubNone\t(none)\t(none)\t172.1.0.4/32\t0\t0\t0\toff\n"

	peers := parseWgDump(dump)
	if got := peers["pubLAN"].Endpoint; got != "192.168.31.10:51820" {
		t.Fatalf("LAN endpoint = %q", got)
	}
	if peers["pubLAN"].LastHandshake.IsZero() {
		t.Fatal("LAN handshake should be parsed")
	}
	if got := peers["pubRelay"].Endpoint; got != "" {
		t.Fatalf("loopback relay endpoint should be empty, got %q", got)
	}
	if got := peers["pubNone"].Endpoint; got != "" {
		t.Fatalf("(none) endpoint should be empty, got %q", got)
	}
}
