package auditwatch

import (
	"testing"
	"time"
)

func TestParseLineFull(t *testing.T) {
	line := `<182>1 2026-07-21T20:26:33.417Z tesp7nsx2p00001 NSX 4816 - [nsx@6876 audit="true" comp="nsx-manager" reqId="9c8f" subcomp="http" username="admin"] UserName="admin", ModuleName="common-services", Operation="GetTransportNodeStatus", Operation status="success", Src="10.118.14.73"`
	e, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected line to parse")
	}
	if e.Operation != "GetTransportNodeStatus" {
		t.Errorf("operation = %q", e.Operation)
	}
	if e.User != "admin" {
		t.Errorf("user = %q", e.User)
	}
	if e.Src != "10.118.14.73" {
		t.Errorf("src = %q", e.Src)
	}
	if e.Status != "success" {
		t.Errorf("status = %q", e.Status)
	}
	want := time.Date(2026, 7, 21, 20, 26, 33, 417000000, time.UTC)
	if !e.TS.Equal(want) {
		t.Errorf("ts = %v, want %v", e.TS, want)
	}
}

func TestParseLineSingleQuotesAndSourceAddress(t *testing.T) {
	line := `2026-07-21T08:29:01Z host NSX 1 - [nsx@6876 audit="true"] UserName='svc-orq', Operation='RefreshRealizedState', Operation status='failure', Source address: 10.118.16.61`
	e, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected line to parse")
	}
	if e.Operation != "RefreshRealizedState" || e.User != "svc-orq" || e.Src != "10.118.16.61" || e.Status != "failure" {
		t.Errorf("parsed = %+v", e)
	}
}

func TestParseLineFallbacks(t *testing.T) {
	// Sem UserName= mas com username= no structured-data; sem Src.
	line := `2026-07-21T08:29:01.000Z host NSX 1 - [nsx@6876 audit="true" username="admin"] Operation="ListLogicalRouters", Operation status="success"`
	e, ok := ParseLine(line)
	if !ok {
		t.Fatal("expected line to parse")
	}
	if e.User != "admin" {
		t.Errorf("user = %q", e.User)
	}
	if e.Src != "unknown" {
		t.Errorf("src = %q, want unknown", e.Src)
	}
}

func TestParseLineSkipsNonAudit(t *testing.T) {
	if _, ok := ParseLine(`2026-07-21T08:29:01Z host NSX 1 - some unrelated log line`); ok {
		t.Error("line without Operation should not parse")
	}
	if _, ok := ParseLine(``); ok {
		t.Error("empty line should not parse")
	}
}

func TestCursorNoDoubleCount(t *testing.T) {
	mk := func(ts string, raw string) Entry {
		tt, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			t.Fatal(err)
		}
		return Entry{TS: tt, Raw: raw}
	}
	e1 := mk("2026-07-21T10:00:00Z", "a")
	e2 := mk("2026-07-21T10:00:05Z", "b")
	e3 := mk("2026-07-21T10:00:05Z", "c") // mesmo ts do e2, linha diferente
	e4 := mk("2026-07-21T10:00:09Z", "d")

	cur := &nodeCursor{}
	cur.advanceTo([]Entry{e1, e2})
	cur.baselined = true

	// Segundo poll relê e1/e2 e traz e3 (mesmo ts de e2) e e4.
	fresh := cur.filterNew([]Entry{e1, e2, e3, e4})
	if len(fresh) != 2 || fresh[0].Raw != "c" || fresh[1].Raw != "d" {
		t.Errorf("fresh = %+v, want [c d]", fresh)
	}
	cur.advanceTo([]Entry{e1, e2, e3, e4})

	// Terceiro poll sem novidade.
	if fresh := cur.filterNew([]Entry{e2, e3, e4}); len(fresh) != 0 {
		t.Errorf("expected no fresh entries, got %+v", fresh)
	}
}
