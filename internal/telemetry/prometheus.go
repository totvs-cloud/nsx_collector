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
)
