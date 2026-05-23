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
)
