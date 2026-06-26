package nsx

import "testing"

func TestOperationalSeverity(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		summary   string
		vendor    string
		want      string
	}{
		{"nic down degraded to warning", "Edge NIC Link Status Down", "", "CRITICAL", "WARNING"},
		{"heavy volume warning", "Heavy Volume Of Alarms", "", "CRITICAL", "WARNING"},
		{"password expired high", "Password Expired", "", "CRITICAL", "HIGH"},
		{"disk usage high", "Edge Disk Usage Very High", "", "CRITICAL", "HIGH"},
		{"maximum capacity high", "Maximum Capacity", "", "CRITICAL", "HIGH"},
		{"rules limit high", "Rules Limit Per Edge Exceeded", "", "CRITICAL", "HIGH"},
		{"service router limit high", "Service Router Limit Per Edge Exceeded", "", "CRITICAL", "HIGH"},
		{"application crashed high", "Application Crashed", "", "CRITICAL", "HIGH"},
		{"transport channel stays critical", "Management Channel To Transport Node Down Long", "", "CRITICAL", "CRITICAL"},
		{"manager channel stays critical", "Management Channel To Manager Node Down Long", "", "CRITICAL", "CRITICAL"},
		{"failure domain stays critical", "Failure Domain Down", "", "CRITICAL", "CRITICAL"},
		{"cert self-signed to warning", "Certificate Expired", "A self-signed certificate has expired for Transport node", "CRITICAL", "WARNING"},
		{"cert ca-signed stays critical", "Certificate Expired", "A certificate has expired for the API/UI service", "CRITICAL", "CRITICAL"},
		{"case insensitive match", "edge nic link status down", "", "CRITICAL", "WARNING"},
		{"unknown event keeps vendor", "Some Brand New Event", "", "MEDIUM", "MEDIUM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &Alarm{
				EventTypeDisplayName: tc.eventType,
				Summary:              tc.summary,
				Severity:             tc.vendor,
			}
			if got := a.OperationalSeverity(); got != tc.want {
				t.Errorf("OperationalSeverity() = %q, want %q", got, tc.want)
			}
		})
	}
}
