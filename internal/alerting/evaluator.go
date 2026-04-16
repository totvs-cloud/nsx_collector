package alerting

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

type AlertLevel int

const (
	AlertInfo    AlertLevel = iota
	AlertWarning
)

type sample struct {
	rxUtilPct float64
	txUtilPct float64
	ts        time.Time
}

// Evaluator checks interface utilization against thresholds and posts alerts to Slack.
type Evaluator struct {
	slack    *SlackClient
	logger   *zap.Logger

	mu       sync.Mutex
	// cooldown tracks the last alert time per interface to avoid flooding
	cooldown map[string]time.Time
	// history stores recent samples for average calculation
	history  map[string][]sample

	cooldownDuration time.Duration
	avgWindow        time.Duration
	avgThreshold     float64
}

func NewEvaluator(slack *SlackClient, logger *zap.Logger) *Evaluator {
	return &Evaluator{
		slack:            slack,
		logger:           logger,
		cooldown:         make(map[string]time.Time),
		history:          make(map[string][]sample),
		cooldownDuration: 15 * time.Minute,
		avgWindow:        5 * time.Minute,
		avgThreshold:     80,
	}
}

// Evaluate checks a single interface sample against thresholds.
// - WARNING (immediate): any direction >= 99%
// - INFORMATION: 5-minute average above avgThreshold
func (e *Evaluator) Evaluate(site, nodeName, ifaceID string, rxUtilPct, txUtilPct float64, linkSpeedMbps int64, rxBps, txBps float64) {
	key := nodeName + ":" + ifaceID
	now := time.Now()

	e.mu.Lock()
	defer e.mu.Unlock()

	e.addSample(key, rxUtilPct, txUtilPct, now)

	maxUtil := rxUtilPct
	direction := "RX"
	bps := rxBps
	if txUtilPct > maxUtil {
		maxUtil = txUtilPct
		direction = "TX"
		bps = txBps
	}

	if maxUtil >= 99 {
		if e.canAlert(key, now) {
			msg := formatWarning(site, nodeName, ifaceID, direction, bps, maxUtil, linkSpeedMbps)
			if err := e.slack.Post(msg); err != nil {
				e.logger.Error("slack alert failed", zap.Error(err))
			} else {
				e.cooldown[key] = now
				e.logger.Warn("capacity warning sent",
					zap.String("node", nodeName),
					zap.String("interface", ifaceID),
					zap.Float64("util_pct", maxUtil),
				)
			}
		}
		return
	}

	avgRx, avgTx := e.avgUtil(key, now)
	avgMax := avgRx
	avgDir := "RX"
	avgBps := rxBps
	if avgTx > avgMax {
		avgMax = avgTx
		avgDir = "TX"
		avgBps = txBps
	}

	if avgMax >= e.avgThreshold {
		if e.canAlert(key, now) {
			msg := formatInfo(site, nodeName, ifaceID, avgDir, avgBps, avgMax, linkSpeedMbps)
			if err := e.slack.Post(msg); err != nil {
				e.logger.Error("slack info alert failed", zap.Error(err))
			} else {
				e.cooldown[key] = now
				e.logger.Info("capacity info sent",
					zap.String("node", nodeName),
					zap.String("interface", ifaceID),
					zap.Float64("avg_util_pct", avgMax),
				)
			}
		}
	}
}

func (e *Evaluator) canAlert(key string, now time.Time) bool {
	last, ok := e.cooldown[key]
	if !ok {
		return true
	}
	return now.Sub(last) >= e.cooldownDuration
}

func (e *Evaluator) addSample(key string, rxPct, txPct float64, now time.Time) {
	e.history[key] = append(e.history[key], sample{rxUtilPct: rxPct, txUtilPct: txPct, ts: now})
	cutoff := now.Add(-e.avgWindow)
	samples := e.history[key]
	i := 0
	for i < len(samples) && samples[i].ts.Before(cutoff) {
		i++
	}
	if i > 0 {
		e.history[key] = samples[i:]
	}
}

func (e *Evaluator) avgUtil(key string, now time.Time) (float64, float64) {
	samples := e.history[key]
	if len(samples) == 0 {
		return 0, 0
	}
	var sumRx, sumTx float64
	for _, s := range samples {
		sumRx += s.rxUtilPct
		sumTx += s.txUtilPct
	}
	n := float64(len(samples))
	return sumRx / n, sumTx / n
}

func formatBps(bps float64) string {
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.1f Gbps", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1f Mbps", bps/1e6)
	case bps >= 1e3:
		return fmt.Sprintf("%.1f Kbps", bps/1e3)
	default:
		return fmt.Sprintf("%.0f bps", bps)
	}
}

func formatWarning(site, nodeName, ifaceID, direction string, bps, utilPct float64, linkSpeedMbps int64) string {
	return fmt.Sprintf(
		":red_circle: *NSX Edge Capacity WARNING*\n%s\n\n`%s` @ `%s` (%s)\n%s: %s / %d Gbps (*%.1f%%*)\nLink Speed: %d Mbps",
		time.Now().Format("02/01/2006 15:04:05"),
		ifaceID, nodeName, site,
		direction, formatBps(bps), linkSpeedMbps/1000, utilPct,
		linkSpeedMbps,
	)
}

func formatInfo(site, nodeName, ifaceID, direction string, bps, avgUtilPct float64, linkSpeedMbps int64) string {
	return fmt.Sprintf(
		":large_blue_circle: *NSX Edge Capacity INFO*\n%s\n\n`%s` @ `%s` (%s)\n%s media 5min: %s / %d Gbps (*%.1f%%*)\nLink Speed: %d Mbps",
		time.Now().Format("02/01/2006 15:04:05"),
		ifaceID, nodeName, site,
		direction, formatBps(bps), linkSpeedMbps/1000, avgUtilPct,
		linkSpeedMbps,
	)
}
