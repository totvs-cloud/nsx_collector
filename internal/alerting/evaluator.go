package alerting

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.uber.org/zap"
)

// utilReader queries InfluxDB for the already-aggregated RX/TX utilization
// used by the Grafana panels, so the alert evaluates the exact same numbers
// the dashboard shows.
type utilReader interface {
	EdgeUtilAvg(ctx context.Context, site, nodeName, ifaceID, window string) (float64, float64, error)
}

type GrafanaConfig struct {
	RenderURL    string // e.g. http://10.114.35.75:3000
	DashboardURL string // e.g. http://network-grafana.cloudtotvs.com.br:3000/d/ffjaqhj6lei2ob/nsx-edge-bandwidth
	APIKey       string
	RxPanelID    string // RX Utilization panel ID
	TxPanelID    string // TX Utilization panel ID
}

type Evaluator struct {
	slack   *SlackClient
	grafana *GrafanaConfig
	reader  utilReader
	logger  *zap.Logger

	mu       sync.Mutex
	cooldown map[string]time.Time

	cooldownDuration time.Duration
	avgWindow        string  // Flux window literal, e.g. "15m"
	warnThreshold    float64
}

// NewEvaluator builds an evaluator. The reader is mandatory: without it the
// alert cannot mirror Grafana's aggregated values and would fall back to
// instantaneous samples (the bug this package used to have).
func NewEvaluator(slack *SlackClient, grafana *GrafanaConfig, reader utilReader, logger *zap.Logger) *Evaluator {
	return &Evaluator{
		slack:            slack,
		grafana:          grafana,
		reader:           reader,
		logger:           logger,
		cooldown:         make(map[string]time.Time),
		cooldownDuration: 3 * time.Minute,
		// 10-minute window: saturação precisa estar sustentada por ~10min
		// pra alertar. Pega problema real rápido e ignora bursts curtos
		// que apareciam como picos finos no Grafana.
		avgWindow:     "10m",
		warnThreshold: 90,
	}
}

// Evaluate checks utilization and sends alerts.
// Instead of using the instantaneous rate just computed by the collector,
// it queries InfluxDB with the same aggregation Grafana uses, so the alert
// fires on the exact value the dashboard displays.
// rxErrors/txErrors are cumulative error rates (errors/sec) from the collector.
func (e *Evaluator) Evaluate(site, nodeName, ifaceID string, rxUtilPct, txUtilPct float64, linkSpeedMbps int64, rxBps, txBps float64, rxErrors, txErrors int64) {
	if e.reader == nil {
		// Without a reader we refuse to alert on raw/instantaneous samples;
		// that mode caused all the false positives this package used to have.
		return
	}

	key := nodeName + ":" + ifaceID
	now := time.Now()

	e.mu.Lock()
	if !e.canAlert(key, now) {
		e.mu.Unlock()
		return
	}
	e.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	avgRx, avgTx, err := e.reader.EdgeUtilAvg(ctx, site, nodeName, ifaceID, e.avgWindow)
	if err != nil {
		e.logger.Warn("grafana aggregated query failed",
			zap.String("node", nodeName),
			zap.String("interface", ifaceID),
			zap.Error(err),
		)
		return
	}

	maxUtil := avgRx
	direction := "RX"
	bps := rxBps
	if avgTx > maxUtil {
		maxUtil = avgTx
		direction = "TX"
		bps = txBps
	}

	if maxUtil < e.warnThreshold {
		return
	}

	e.mu.Lock()
	// Re-check cooldown after the async query: another goroutine may have
	// fired an alert for the same key while we were waiting on InfluxDB.
	if !e.canAlert(key, now) {
		e.mu.Unlock()
		return
	}
	e.cooldown[key] = now
	e.mu.Unlock()

	msg := e.formatAlert(site, nodeName, ifaceID, direction, bps, maxUtil, linkSpeedMbps, rxErrors, txErrors)
	ts, err := e.slack.Post(msg)
	if err != nil {
		e.logger.Error("slack alert failed", zap.Error(err))
		return
	}
	e.logger.Warn("capacity alert sent",
		zap.String("node", nodeName),
		zap.String("interface", ifaceID),
		zap.String("direction", direction),
		zap.Float64("util_pct", maxUtil),
		zap.String("window", e.avgWindow),
	)

	go e.attachScreenshot(site, nodeName, direction, ts)
}

func (e *Evaluator) canAlert(key string, now time.Time) bool {
	last, ok := e.cooldown[key]
	if !ok {
		return true
	}
	return now.Sub(last) >= e.cooldownDuration
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
