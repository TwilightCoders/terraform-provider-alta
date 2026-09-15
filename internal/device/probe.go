package device

import (
	"bufio"
	"bytes"
	"strings"
)

// NoncePlaceholder in ProbeSpec.DNSName is replaced with a fresh random label on every
// probe, so the lookup can never be answered from a cache.
const NoncePlaceholder = "{nonce}"

// ProbeSpec says what healthy looks like from the router.
type ProbeSpec struct {
	// WANTarget is pinged to prove internet reachability.
	WANTarget string
	// DNSServer and DNSName prove end-to-end recursion: DNSName should be a name under a
	// wildcard record, containing NoncePlaceholder, so it resolves only via a real lookup.
	DNSServer string
	DNSName   string
	// LANTargets are pinged to prove LAN reachability.
	LANTargets []string
}

// ProbeResult is the outcome of one probe.
type ProbeResult struct {
	Name   string
	OK     bool
	Detail string
}

// ProbeReport is the outcome of a probe run.
type ProbeReport []ProbeResult

// Regressions returns probes that passed in baseline and fail now. Probes that were
// already failing before a change are not the change's fault.
func (r ProbeReport) Regressions(baseline ProbeReport) []ProbeResult {
	passed := map[string]bool{}
	for _, b := range baseline {
		passed[b.Name] = b.OK
	}
	var out []ProbeResult
	for _, p := range r {
		if !p.OK && passed[p.Name] {
			out = append(out, p)
		}
	}
	return out
}

// Failures returns the probes that failed.
func (r ProbeReport) Failures() []ProbeResult {
	var out []ProbeResult
	for _, p := range r {
		if !p.OK {
			out = append(out, p)
		}
	}
	return out
}

func parseProbeReport(out []byte) ProbeReport {
	var report ProbeReport
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "\t", 3)
		if len(parts) < 2 {
			continue
		}
		r := ProbeResult{Name: parts[0], OK: parts[1] == "ok"}
		if len(parts) == 3 {
			r.Detail = parts[2]
		}
		report = append(report, r)
	}
	return report
}
