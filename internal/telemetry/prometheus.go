package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CollectCyclesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_collect_cycles_total",
		Help: "Total number of collection cycles completed.",
	}, []string{"site"})

	CollectDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nsx_collector_collect_duration_seconds",
		Help:    "Duration of each collection cycle.",
		Buckets: prometheus.DefBuckets,
	}, []string{"site"})

	CollectErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_collect_errors_total",
		Help: "Total number of collection errors.",
	}, []string{"site", "component"})

	PointsWritten = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_points_written_total",
		Help: "Total number of InfluxDB points written.",
	}, []string{"site"})

	// HA-state collection telemetry
	HAPolls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_ha_polls_total",
		Help: "Total HA collection cycles (one per gated tick of intervals.ha).",
	}, []string{"site"})

	HAChanges = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_ha_changes_total",
		Help: "Total HA failover events emitted (majority of observed T1s shifted ACTIVE).",
	}, []string{"site", "t0_cluster"})

	HAObservedT1s = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nsx_collector_ha_observed_t1s",
		Help: "Current size of the HA-watch inventory per T0 edge cluster.",
	}, []string{"site", "t0_cluster"})

	HAWatchSubstitutions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_ha_watch_substitutions_total",
		Help: "Times a missing T1 in the HA-watch inventory was replaced by a fresh pick.",
	}, []string{"site", "t0_cluster"})

	// T1Watch — new/deleted Tier-1 detection + Slack notifications
	T1Created = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_t1_created_total",
		Help: "Total T1 gateways detected as newly created by the t1watch differ.",
	}, []string{"site"})

	T1Deleted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_t1_deleted_total",
		Help: "Total T1 gateways detected as removed by the t1watch differ.",
	}, []string{"site"})

	T1NotifySent = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_t1_notify_sent_total",
		Help: "Total Slack notifications successfully posted for T1 lifecycle events.",
	}, []string{"site"})

	T1NotifyErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_t1_notify_errors_total",
		Help: "Total Slack notification failures for T1 lifecycle events.",
	}, []string{"site"})

	T1KnownGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nsx_collector_t1_known",
		Help: "Current size of the t1watch snapshot per site.",
	}, []string{"site"})

	// Capacity extensions — LB credits, segments, NAT/FW per T1, groups
	LBCreditsPct = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "nsx_collector_lb_credits_usage_pct",
		Help: "Current LB credits usage percentage per site (manager scope).",
	}, []string{"site"})

	CapacityExtrasPolls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "nsx_collector_capacity_extras_polls_total",
		Help: "Total invocations of extended capacity collections (segments/NAT/FW/groups).",
	}, []string{"site", "kind"})
)
