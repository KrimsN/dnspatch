package netif

// Config holds the parameters of the interface retriever.
type Config struct {
	Name    string `toml:"name,required" doc:"Name of the network interface to read, for example eth0, wan or Ethernet. Of the addresses on it, only public ones are considered: loopback, link-local, private (10/8, 172.16/12, 192.168/16, fc00::/7) and shared CGNAT (100.64/10) addresses are skipped. If several remain, the numerically lowest one is used, so the choice is stable; narrow it down with network"`
	Family  string `toml:"family" default:"ipv6" doc:"IP family to read: ipv4, ipv6, or dual to read both. dual fails when the interface has no suitable address of either family"`
	Network string `toml:"network" doc:"Optional CIDR prefix, for example 2001:db8:1234::/64: only addresses inside it are considered. Use it to pick one of several prefixes on the interface, such as a delegated prefix, or to tell a stable address from a temporary one"`
}
