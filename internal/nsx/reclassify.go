package nsx

import "strings"

// reclassify.go applies the operational severity reclassification approved by the
// NOC ("Reclassificacao de Alertas", coluna C = "Severity Operacional Sugerida").
//
// NSX ships every fault with a vendor severity (CRITICAL/HIGH/MEDIUM/LOW) that is
// often too aggressive for our environment — e.g. a single NIC down in a 2-NIC LAG
// is degradation, not an outage. We remap the vendor severity to an operational one
// keyed on the alarm's event type so Grafana panels reflect real urgency instead of
// drowning the team in CRITICALs.
//
// Matching is on EventTypeDisplayName, which corresponds to the "Tipo de Evento"
// column of the approved spreadsheet. Event types not listed here keep their vendor
// severity unchanged.

// operationalSeverityByEventType maps the NSX event_type_display_name to the
// approved operational severity. Keys are compared case-insensitively after trimming.
//
// "Certificate Expired" is intentionally absent: ROOT/CA-signed certs stay CRITICAL
// while self-signed internal certs drop to WARNING, a distinction that depends on the
// summary text and is handled in OperationalSeverity below.
var operationalSeverityByEventType = map[string]string{
	"password expired":                               "HIGH",
	"service router limit per edge exceeded":         "HIGH",
	"edge nic link status down":                      "WARNING",
	"edge disk usage very high":                      "HIGH",
	"heavy volume of alarms":                         "WARNING",
	"management channel to transport node down long": "CRITICAL",
	"application crashed":                            "HIGH",
	"maximum capacity":                               "HIGH",
	"management channel to manager node down long":   "CRITICAL",
	"failure domain down":                            "CRITICAL",
	"rules limit per edge exceeded":                  "HIGH",
}

// OperationalSeverity returns the reclassified severity for an alarm. It never
// returns an empty string: event types without an approved override fall back to the
// alarm's vendor severity.
func (a *Alarm) OperationalSeverity() string {
	key := strings.ToLower(strings.TrimSpace(a.EventTypeDisplayName))

	// Certificate Expired: self-signed internal certs (Corfu/APH_TN/CCP) are noise
	// and drop to WARNING; CA-signed certs (API/UI/inter-site) stay CRITICAL.
	if key == "certificate expired" {
		summary := strings.ToLower(a.Summary)
		if strings.Contains(summary, "self-signed") ||
			strings.Contains(summary, "self signed") ||
			strings.Contains(summary, "self_signed") {
			return "WARNING"
		}
		return a.Severity
	}

	if sev, ok := operationalSeverityByEventType[key]; ok {
		return sev
	}
	return a.Severity
}
