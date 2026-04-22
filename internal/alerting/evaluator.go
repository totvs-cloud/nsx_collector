package alerting

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
)

type sample struct {
	rxUtilPct float64
	txUtilPct float64
	ts        time.Time
}

type GrafanaConfig struct {
	RenderURL    string // e.g. http://10.114.35.75:3000
	DashboardURL string // e.g. http://network-grafana.cloudtotvs.com.br:3000/d/ffjaqhj6lei2ob/nsx-edge-bandwidth
	APIKey       string
	RxPanelID    string // RX Utilization panel ID
	TxPanelID    string // TX Utilization panel ID
}

type Evaluator struct {
	slack    *SlackClient
	grafana  *GrafanaConfig
	logger   *zap.Logger

	mu       sync.Mutex
	cooldown map[string]time.Time
	history  map[string][]sample

	cooldownDuration time.Duration
	avgWindow        time.Duration
	avgThreshold     float64
	warnThreshold    float64
	minSamples       int
}

func NewEvaluator(slack *SlackClient, grafana *GrafanaConfig, logger *zap.Logger) *Evaluator {
	return &Evaluator{
		slack:            slack,
		grafana:          grafana,
		logger:           logger,
		cooldown:         make(map[string]time.Time),
		history:          make(map[string][]sample),
		cooldownDuration: 3 * time.Minute,
		avgWindow:        5 * time.Minute,
		avgThreshold:     80,
		warnThreshold:    90,
		// Require at least 3 samples (~2 minutes at 40s polling) before
		// alerting, so a single-cycle burst cannot trip the average.
		minSamples: 3,
	}
}

// Evaluate checks utilization and sends alerts.
// rxErrors/txErrors are cumulative error rates (errors/sec) from the collector.
func (e *Evaluator) Evaluate(site, nodeName, ifaceID string, rxUtilPct, txUtilPct float64, linkSpeedMbps int64, rxBps, txBps float64, rxErrors, txErrors int64) {
	key := nodeName + ":" + ifaceID
	now := time.Now()

	e.mu.Lock()
	defer e.mu.Unlock()

	e.addSample(key, rxUtilPct, txUtilPct, now)

	// Need enough samples in the window to avoid alerting on a single burst.
	if len(e.history[key]) < e.minSamples {
		return
	}

	// Use average utilization over the window instead of instantaneous value.
	// This matches Grafana's aggregateWindow(fn: mean) and avoids false alerts
	// from short bursts that disappear in the averaged view.
	avgRx, avgTx := e.avgUtil(key)

	maxUtil := avgRx
	direction := "RX"
	bps := rxBps
	if avgTx > maxUtil {
		maxUtil = avgTx
		direction = "TX"
		bps = txBps
	}

	if maxUtil >= e.warnThreshold && e.canAlert(key, now) {
		msg := e.formatAlert(site, nodeName, ifaceID, direction, bps, maxUtil, linkSpeedMbps, rxErrors, txErrors)
		ts, err := e.slack.Post(msg)
		if err != nil {
			e.logger.Error("slack alert failed", zap.Error(err))
			return
		}
		e.cooldown[key] = now
		e.logger.Warn("capacity alert sent",
			zap.String("node", nodeName),
			zap.String("interface", ifaceID),
			zap.Float64("util_pct", maxUtil),
		)

		go e.attachScreenshot(site, nodeName, direction, ts)
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

// avgUtil returns the mean RX and TX utilization over the stored samples for key.
func (e *Evaluator) avgUtil(key string) (float64, float64) {
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

func (e *Evaluator) formatAlert(site, nodeName, ifaceID, direction string, bps, utilPct float64, linkSpeedMbps int64, rxErrors, txErrors int64) string {
	dashLink := e.dashboardLink(nodeName)

	errMsg := "Nenhum"
	if rxErrors > 0 || txErrors > 0 {
		errMsg = fmt.Sprintf("RX: %d/s | TX: %d/s", rxErrors, txErrors)
	}

	icon := ":warning:"
	level := "WARNING"
	if utilPct >= 99 {
		icon = ":red_circle:"
		level = "CRITICAL"
	}

	return fmt.Sprintf(
		"%s *NSX Edge Capacity %s*\n%s\n\n"+
			"*Edge Node:* `%s`\n"+
			"*Interface:* `%s`\n"+
			"*Site:* %s\n\n"+
			"*%s:* %s / %d Gbps (*%.1f%%*)\n"+
			"*Link Speed:* %d Mbps\n"+
			"*Erros:* %s\n\n"+
			":chart_with_upwards_trend: <%s|Ver no Grafana>",
		icon, level,
		time.Now().Format("02/01/2006 15:04:05"),
		nodeName,
		ifaceID,
		site,
		direction, formatBps(bps), linkSpeedMbps/1000, utilPct,
		linkSpeedMbps,
		errMsg,
		dashLink,
	)
}

func (e *Evaluator) dashboardLink(nodeName string) string {
	if e.grafana == nil || e.grafana.DashboardURL == "" {
		return ""
	}
	return fmt.Sprintf("%s?orgId=1&var-site=All&var-edge_node=%s",
		e.grafana.DashboardURL,
		url.QueryEscape(nodeName),
	)
}

func (e *Evaluator) attachScreenshot(site, nodeName, direction, threadTS string) {
	if e.grafana == nil || e.grafana.RenderURL == "" {
		return
	}

	// Select panel matching the saturating direction so the screenshot
	// corresponds to the alert (RX alert → RX graph, TX alert → TX graph).
	panelID := e.grafana.RxPanelID
	if direction == "TX" && e.grafana.TxPanelID != "" {
		panelID = e.grafana.TxPanelID
	}
	if panelID == "" {
		return
	}

	now := time.Now()
	from := now.Add(-1 * time.Hour).UnixMilli()
	to := now.UnixMilli()

	renderURL := fmt.Sprintf(
		"%s/render/d-solo/ffjaqhj6lei2ob/nsx-edge-bandwidth?orgId=1&panelId=%s&var-site=%s&var-edge_node=%s&width=1000&height=500&from=%d&to=%d",
		e.grafana.RenderURL,
		panelID,
		url.QueryEscape(site),
		url.QueryEscape(nodeName),
		from, to,
	)

	req, err := http.NewRequest("GET", renderURL, nil)
	if err != nil {
		e.logger.Error("grafana render request failed", zap.Error(err))
		return
	}
	if e.grafana.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.grafana.APIKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		e.logger.Error("grafana render failed", zap.Error(err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		e.logger.Warn("grafana render non-200", zap.Int("status", resp.StatusCode))
		return
	}

	img, err := io.ReadAll(resp.Body)
	if err != nil {
		e.logger.Error("grafana render read failed", zap.Error(err))
		return
	}

	filename := fmt.Sprintf("%s-util-%s-%s.png", direction, nodeName, now.Format("150405"))
	if err := e.slack.UploadImage(threadTS, filename, direction+" Utilization - "+nodeName, img); err != nil {
		e.logger.Error("slack screenshot upload failed", zap.Error(err))
	} else {
		e.logger.Info("screenshot attached to alert", zap.String("node", nodeName))
	}
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
