package t1watch

import (
	"fmt"

	"go.uber.org/zap"
)

// SlackPoster is the minimal interface needed from the Slack client.
// internal/alerting.SlackClient satisfies it (Post(text) (ts, err)).
type SlackPoster interface {
	Post(text string) (string, error)
}

// ChannelSlackPoster wraps a per-channel post. internal/alerting.SlackClient is
// bound to one channel at construction; if T1Watch.SlackChannel overrides the
// default, wrap it in a SlackClient built with that channel instead.
type ChannelSlackPoster struct {
	Client SlackPoster
}

// Notifier formats and posts T1 lifecycle events to Slack.
// Events on "deleted" are silenced by default — set EmitDeleted=true to
// also post deletion messages.
type Notifier struct {
	Slack       SlackPoster
	Site        string
	Logger      *zap.Logger
	EmitDeleted bool
}

// FormatCreated produces the literal message text requested by the user:
//   "T1 <name> criado no CLUSTER <cluster>, Agora sao <vrf_count> do limite
//    preestabelecido de <vrf_limit> na vrf <vrf_name>, de um total de <site_total>
//    t1 no <site>."
// For a T1 attached to a regular T0 (not a VRF) we say "no t0 <name>" instead
// of "na vrf <name>" so the message stays accurate.
func FormatCreated(ev Event, site string) string {
	cluster := ev.T1.EdgeClusterName
	if cluster == "" {
		cluster = ev.T1.EdgeClusterID
	}
	if cluster == "" {
		cluster = "(sem edge cluster)"
	}
	parentLabel := "vrf"
	if ev.T1.ParentKind != "vrf" {
		parentLabel = "t0"
	}
	return fmt.Sprintf(
		"T1 %s criado no CLUSTER %s, Agora sao %d do limite preestabelecido de %d na %s %s, de um total de %d t1 no %s.",
		ev.T1.Name, cluster,
		ev.VRFT1CountAfter, ev.VRFT1Limit,
		parentLabel, ev.T1.ParentT0Name,
		ev.SiteT1Total, site,
	)
}

// FormatDeleted is used when EmitDeleted is enabled.
func FormatDeleted(ev Event, site string) string {
	parentLabel := "vrf"
	if ev.T1.ParentKind != "vrf" {
		parentLabel = "t0"
	}
	return fmt.Sprintf(
		"T1 %s removido da %s %s, Agora sao %d (limite %d), total de %d t1 no %s.",
		ev.T1.Name, parentLabel, ev.T1.ParentT0Name,
		ev.VRFT1CountAfter, ev.VRFT1Limit,
		ev.SiteT1Total, site,
	)
}

// Send posts a slice of events to Slack, logging errors per event without
// aborting the rest. Returns the number of messages successfully posted.
func (n *Notifier) Send(events []Event) (sent int, errs int) {
	if n == nil || n.Slack == nil {
		return 0, 0
	}
	for _, ev := range events {
		var msg string
		switch ev.Kind {
		case "created":
			msg = FormatCreated(ev, n.Site)
		case "deleted":
			if !n.EmitDeleted {
				continue
			}
			msg = FormatDeleted(ev, n.Site)
		default:
			continue
		}
		if _, err := n.Slack.Post(msg); err != nil {
			errs++
			if n.Logger != nil {
				n.Logger.Warn("t1watch slack post failed",
					zap.String("event", ev.Kind),
					zap.String("t1_name", ev.T1.Name),
					zap.Error(err),
				)
			}
			continue
		}
		sent++
		if n.Logger != nil {
			n.Logger.Info("t1watch slack posted",
				zap.String("event", ev.Kind),
				zap.String("t1_name", ev.T1.Name),
				zap.String("parent", ev.T1.ParentT0Name),
				zap.Int64("vrf_count_after", ev.VRFT1CountAfter),
				zap.Int64("vrf_limit", ev.VRFT1Limit),
			)
		}
	}
	return sent, errs
}
