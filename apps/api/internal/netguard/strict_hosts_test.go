package netguard

// strictHostCases is a shared table of (host, allow) pairs for the strict-policy
// HTTP client that must refuse every non-publicly-routable address.
// A strict-policy test will range over it and call IsPublic after resolving the host
// to a netip.Addr (or parsing it directly for numeric forms).
var strictHostCases = []struct {
	host  string
	allow bool
	why   string
}{
	// ── IPv4 private (RFC 1918) ────────────────────────────────────────────────
	{"10.0.0.1", false, "RFC 1918 10/8 first address"},
	{"10.255.255.255", false, "RFC 1918 10/8 last address"},
	{"172.16.0.1", false, "RFC 1918 172.16/12 first address"},
	{"172.31.255.255", false, "RFC 1918 172.16/12 last address"},
	{"172.20.10.1", false, "RFC 1918 172.16/12 mid-range"},
	{"192.168.0.1", false, "RFC 1918 192.168/16 first address"},
	{"192.168.1.1", false, "RFC 1918 192.168/16 common router"},
	{"192.168.255.255", false, "RFC 1918 192.168/16 last address"},

	// ── Loopback 127/8 ────────────────────────────────────────────────────────
	{"127.0.0.1", false, "IPv4 loopback canonical"},
	{"127.0.0.254", false, "IPv4 loopback non-canonical"},
	{"127.255.255.255", false, "IPv4 loopback last address"},

	// ── Link-local 169.254/16 (includes AWS metadata) ─────────────────────────
	{"169.254.0.1", false, "link-local first usable"},
	{"169.254.169.254", false, "AWS/GCP/Azure IMDS endpoint"},
	{"169.254.255.255", false, "link-local last address"},

	// ── CGNAT 100.64/10 (RFC 6598) ────────────────────────────────────────────
	{"100.64.0.1", false, "CGNAT first address"},
	{"100.127.255.255", false, "CGNAT last address"},
	{"100.100.100.100", false, "CGNAT mid-range"},

	// ── 0.0.0.0/8 ─────────────────────────────────────────────────────────────
	{"0.0.0.0", false, "unspecified / this-network"},
	{"0.1.2.3", false, "0/8 non-zero host"},
	{"0.255.255.255", false, "0/8 last address"},

	// ── Documentation ranges (RFC 5737) ───────────────────────────────────────
	{"192.0.2.1", false, "TEST-NET-1 192.0.2/24"},
	{"192.0.2.255", false, "TEST-NET-1 last address"},
	{"198.51.100.1", false, "TEST-NET-2 198.51.100/24"},
	{"198.51.100.255", false, "TEST-NET-2 last address"},
	{"203.0.113.1", false, "TEST-NET-3 203.0.113/24"},
	{"203.0.113.255", false, "TEST-NET-3 last address"},

	// ── Benchmarking 198.18/15 (RFC 2544) ─────────────────────────────────────
	{"198.18.0.1", false, "benchmarking 198.18/15 first"},
	{"198.19.255.255", false, "benchmarking 198.18/15 last"},

	// ── Multicast 224/4 ───────────────────────────────────────────────────────
	{"224.0.0.1", false, "multicast all-hosts"},
	{"239.255.255.255", false, "multicast last address"},

	// ── Reserved / future 240/4 (RFC 1112) ───────────────────────────────────
	{"240.0.0.0", false, "reserved 240/4 first"},
	{"255.255.255.254", false, "reserved 240/4 near-last"},

	// ── Broadcast ─────────────────────────────────────────────────────────────
	{"255.255.255.255", false, "limited broadcast"},

	// ── IPv6 loopback / unspecified ───────────────────────────────────────────
	{"::1", false, "IPv6 loopback"},
	{"::", false, "IPv6 unspecified"},

	// ── IPv6 link-local fe80::/10 ─────────────────────────────────────────────
	{"fe80::1", false, "link-local fe80::1"},
	{"fe80::1%eth0", false, "link-local with zone ID"},
	{"febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff", false, "link-local fe80::/10 last"},

	// ── IPv6 ULA fc00::/7 (RFC 4193) ──────────────────────────────────────────
	{"fc00::1", false, "ULA fc00::/7 first"},
	{"fd00::1", false, "ULA fd::/8 common prefix"},
	{"fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", false, "ULA fc00::/7 last"},

	// ── IPv6 multicast ff00::/8 ───────────────────────────────────────────────
	{"ff02::1", false, "multicast all-nodes link-local"},
	{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", false, "multicast all-bits-set"},

	// ── Documentation 2001:db8::/32 (RFC 3849) ───────────────────────────────
	{"2001:db8::1", false, "IPv6 documentation prefix"},
	{"2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", false, "IPv6 documentation last"},

	// ── NAT64 64:ff9b::/96 wrapping private IPv4 ─────────────────────────────
	{"64:ff9b::10.0.0.1", false, "NAT64 of 10.0.0.1"},
	{"64:ff9b::192.168.1.1", false, "NAT64 of 192.168.1.1"},
	{"64:ff9b::127.0.0.1", false, "NAT64 of 127.0.0.1"},

	// ── IPv4-mapped IPv6 ::ffff:0:0/96 ───────────────────────────────────────
	{"::ffff:127.0.0.1", false, "IPv4-mapped loopback"},
	{"::ffff:10.0.0.1", false, "IPv4-mapped RFC 1918 10/8"},
	{"::ffff:192.168.0.1", false, "IPv4-mapped RFC 1918 192.168/16"},

	// ── 6to4 2002::/16 (RFC 3056) wrapping private IPv4 ─────────────────────
	{"2002:7f00:1::", false, "6to4 of 127.0.0.1"},
	{"2002:0a00:0101::", false, "6to4 of 10.0.1.1"},
	{"2002:c0a8:0101::", false, "6to4 of 192.168.1.1"},

	// ── Teredo 2001::/32 (RFC 4380) ───────────────────────────────────────────
	{"2001:0000::1", false, "Teredo prefix"},

	// ── Non-canonical / obfuscated numeric forms ──────────────────────────────
	{"2130706433", false, "decimal representation of 127.0.0.1"},
	{"0x7f000001", false, "hex representation of 127.0.0.1"},
	{"017700000001", false, "octal representation of 127.0.0.1"},
	{"0177.0.0.1", false, "mixed-octal 0177.0.0.1"},
	{"127.1", false, "compressed dotted 127.1 (loopback)"},
	{"127.0.1", false, "three-part form 127.0.1 (loopback)"},
	{"[::ffff:7f00:1]", false, "bracketed IPv4-mapped loopback hex"},

	// ── ALLOWED: well-known public addresses ──────────────────────────────────
	{"8.8.8.8", true, "Google Public DNS primary"},
	{"8.8.4.4", true, "Google Public DNS secondary"},
	{"1.1.1.1", true, "Cloudflare DNS primary"},
	{"1.0.0.1", true, "Cloudflare DNS secondary"},
	{"93.184.216.34", true, "example.com literal IP"},
	{"198.41.0.4", true, "IANA root-servers.net (a-root)"},
	{"100.128.0.1", true, "just outside CGNAT (100.128/9)"},
	{"172.32.0.1", true, "just outside RFC 1918 172.16/12"},
	{"192.169.0.1", true, "just outside RFC 1918 192.168/16"},
	{"2606:4700:4700::1111", true, "Cloudflare DNS IPv6 primary"},
	{"2001:4860:4860::8888", true, "Google DNS IPv6 primary"},
	{"2606:2800:220:1:248:1893:25c8:1946", true, "example.com literal IPv6"},
}
