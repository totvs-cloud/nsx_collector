package t1watch

import (
	"time"
)

// LiveT1 describes one T1 as observed in the current cycle, already enriched
// with parent T0/VRF and edge cluster identity by the worker.
type LiveT1 struct {
	ID              string
	Name            string
	ParentT0ID      string
	ParentT0Name    string
	ParentKind      string // "vrf" or "t0"
	EdgeClusterID   string
	EdgeClusterName string
}

// Event is one T1 lifecycle event emitted by Detect.
type Event struct {
	Kind            string // "created" or "deleted"
	T1              LiveT1
	OccurredAt      time.Time
	// Counts captured at the moment of detection (for the Slack message body).
	VRFT1CountAfter int64
	VRFT1Limit      int64
	SiteT1Total     int64
}

// Detect updates the snapshot in place against the current live inventory and
// returns events. baselined=false means snapshot file was absent on load —
// baseline only, never emit events. The caller is responsible for SaveSnapshot
// afterwards.
//
// vrfCountsAfter/t0CountsAfter and totalT1 are computed from the live inventory
// by the caller (we receive them here to fill the event payload). A T1 parented
// to a VRF is counted/limited against the VRF maps; one parented to a regular
// T0 against the T0 maps — keyed by the parent display name. vrfLimitFn and
// t0LimitFn are the matching per-parent limit resolvers (default + overrides).
func Detect(
	snapshot *Snapshot,
	live []LiveT1,
	baselined bool,
	vrfCountsAfter map[string]int64,
	t0CountsAfter map[string]int64,
	totalT1 int64,
	vrfLimitFn func(vrfName string) int64,
	t0LimitFn func(t0Name string) int64,
	now time.Time,
) []Event {
	liveByID := make(map[string]LiveT1, len(live))
	for _, t := range live {
		liveByID[t.ID] = t
	}

	// countFor / limitFor pick the VRF or T0 lookup based on the parent kind so
	// T1s under a regular T0 don't report 0 against the (empty) VRF map.
	countFor := func(kind, name string) int64 {
		if kind == "vrf" {
			return vrfCountsAfter[name]
		}
		return t0CountsAfter[name]
	}
	limitFor := func(kind, name string) int64 {
		if kind == "vrf" {
			return vrfLimitFn(name)
		}
		return t0LimitFn(name)
	}

	var events []Event

	// Detect newly created T1s.
	for id, t := range liveByID {
		if _, known := snapshot.Known[id]; known {
			continue
		}
		// New to us: add to snapshot.
		snapshot.Known[id] = T1Info{
			ID:              t.ID,
			Name:            t.Name,
			ParentT0ID:      t.ParentT0ID,
			ParentT0Name:    t.ParentT0Name,
			ParentKind:      t.ParentKind,
			EdgeClusterID:   t.EdgeClusterID,
			EdgeClusterName: t.EdgeClusterName,
			FirstSeen:       now.Unix(),
		}
		if !baselined {
			// First run after fresh install: never emit, just baseline.
			continue
		}
		events = append(events, Event{
			Kind:            "created",
			T1:              t,
			OccurredAt:      now,
			VRFT1CountAfter: countFor(t.ParentKind, t.ParentT0Name),
			VRFT1Limit:      limitFor(t.ParentKind, t.ParentT0Name),
			SiteT1Total:     totalT1,
		})
	}

	// Detect deletions: ids in snapshot but missing from live.
	for id, info := range snapshot.Known {
		if _, alive := liveByID[id]; alive {
			continue
		}
		delete(snapshot.Known, id)
		if !baselined {
			continue
		}
		events = append(events, Event{
			Kind: "deleted",
			T1: LiveT1{
				ID:              info.ID,
				Name:            info.Name,
				ParentT0ID:      info.ParentT0ID,
				ParentT0Name:    info.ParentT0Name,
				ParentKind:      info.ParentKind,
				EdgeClusterID:   info.EdgeClusterID,
				EdgeClusterName: info.EdgeClusterName,
			},
			OccurredAt:      now,
			VRFT1CountAfter: countFor(info.ParentKind, info.ParentT0Name),
			VRFT1Limit:      limitFor(info.ParentKind, info.ParentT0Name),
			SiteT1Total:     totalT1,
		})
	}

	// Refresh edge cluster identity on already-known T1s if the live view
	// differs — keeps the snapshot accurate for future delete payloads.
	for id, info := range snapshot.Known {
		live, ok := liveByID[id]
		if !ok {
			continue
		}
		if live.EdgeClusterID != info.EdgeClusterID ||
			live.EdgeClusterName != info.EdgeClusterName ||
			live.ParentT0Name != info.ParentT0Name ||
			live.Name != info.Name {
			info.Name = live.Name
			info.ParentT0ID = live.ParentT0ID
			info.ParentT0Name = live.ParentT0Name
			info.ParentKind = live.ParentKind
			info.EdgeClusterID = live.EdgeClusterID
			info.EdgeClusterName = live.EdgeClusterName
			snapshot.Known[id] = info
		}
	}

	snapshot.Updated = now
	return events
}

// LimitResolver builds a per-VRF limit lookup function from defaults +
// overrides loaded from config.
func LimitResolver(defaultLimit int64, overrides map[string]int64) func(string) int64 {
	return func(vrfName string) int64 {
		if v, ok := overrides[vrfName]; ok && v > 0 {
			return v
		}
		return defaultLimit
	}
}
