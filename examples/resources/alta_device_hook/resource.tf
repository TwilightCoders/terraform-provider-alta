# Keep one firewall rule in place that another service rebuilds when its interface comes
# up. Two things matter in the body:
#
#   * It is idempotent: it runs on every boot, every configuration push and every ifup.
#   * It returns immediately. Hotplug events are processed one at a time, so a hook that
#     waits inline delays every event behind it. The watch runs detached, under a lock so
#     repeated ifups cannot pile up.
resource "alta_device_hook" "resolver_exemption" {
  name      = "resolver-exemption"
  interface = "wg0"

  script = <<-SH
    # Hold the rule for a minute, detached: the service that owns this chain may rebuild
    # it several seconds after ifup, which would silently drop a single assertion.
    setsid flock -n /tmp/resolver-exemption.lock /bin/sh -c '
      chain=vpn_dns_nat_wg0
      resolver=192.0.2.53
      i=0
      while [ "$i" -lt 60 ]; do
        if iptables -w -t nat -L "$chain" -n >/dev/null 2>&1; then
          iptables -w -t nat -C "$chain" -s "$resolver" -j RETURN 2>/dev/null ||
            iptables -w -t nat -I "$chain" 1 -s "$resolver" -j RETURN
        fi
        sleep 1
        i=$((i + 1))
      done
    ' >/dev/null 2>&1 &
  SH
}
