package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"nsx-collector/internal/alerting"
	"nsx-collector/internal/collector"
	"nsx-collector/internal/config"
	influxpkg "nsx-collector/internal/influxdb"
	"nsx-collector/internal/nsx"
)

func main() {
	configFile := flag.String("config", "/home/nsx_collector/configs/config.yaml", "Path to config file")
	managersFile := flag.String("managers", "/home/nsx_collector/configs/managers.yaml", "Path to managers file")
	envFile := flag.String("env-file", "/home/nsx_collector/.env", "Path to .env file")
	printClusters := flag.Bool("print-clusters", false, "Print T0 edge clusters as JSON and exit (used by scripts/generate-mrpe-ha.sh)")
	flag.Parse()

	// Load .env file
	if err := godotenv.Load(*envFile); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load .env file %s: %v\n", *envFile, err)
	}

	// Load config
	cfg, err := config.LoadConfig(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Load managers
	managers, err := config.LoadManagers(*managersFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load managers: %v\n", err)
		os.Exit(1)
	}

	// One-shot: print T0 edge clusters as JSON and exit.
	// Consumed by scripts/generate-mrpe-ha.sh to render mrpe.cfg.d entries.
	if *printClusters {
		runPrintClusters(managers)
		return
	}

	// Initialize logger
	logger, err := initLogger(cfg.Logging)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	zap.ReplaceGlobals(logger)
	defer logger.Sync()

	// Initialize InfluxDB client
	influxClient := influxdb2.NewClient(cfg.InfluxDB.URL, cfg.InfluxDB.Token)
	defer influxClient.Close()

	writer := influxpkg.NewWriter(influxClient, cfg.InfluxDB.Org, cfg.InfluxDB.Bucket, cfg.InfluxDB.CapacityBucket)
	reader := influxpkg.NewReader(influxClient, cfg.InfluxDB.Org, cfg.InfluxDB.Bucket)

	// Build alert evaluator (nil if Slack not configured)
	var alertEval *alerting.Evaluator
	if cfg.Slack.Enabled && cfg.Slack.Channel != "" {
		slackToken := os.Getenv(cfg.Slack.BotTokenEnv)
		if slackToken != "" {
			slackClient := alerting.NewSlackClient(slackToken, cfg.Slack.Channel)
			var grafanaCfg *alerting.GrafanaConfig
			if cfg.Slack.GrafanaURL != "" {
				grafanaCfg = &alerting.GrafanaConfig{
					RenderURL:    cfg.Slack.GrafanaURL,
					DashboardURL: cfg.Slack.DashboardURL,
					APIKey:       os.Getenv(cfg.Slack.GrafanaKeyEnv),
					RxPanelID:    cfg.Slack.RXUtilPanelID,
					TxPanelID:    cfg.Slack.TXUtilPanelID,
				}
			}
			alertEval = alerting.NewEvaluator(slackClient, grafanaCfg, reader, logger)
			logger.Info("slack alerting enabled", zap.String("channel", cfg.Slack.Channel))
		} else {
			logger.Warn("slack alerting disabled: token env var empty", zap.String("env", cfg.Slack.BotTokenEnv))
		}
	}

	// Build workers (one per manager)
	rateCalc := collector.NewRateCalculator()
	var workers []*collector.Worker
	for _, mgr := range managers {
		workers = append(workers, collector.NewWorker(mgr, writer, cfg.Intervals, cfg.InterfaceSpeeds, rateCalc, alertEval))
		logger.Info("manager registered",
			zap.String("site", mgr.Site),
			zap.String("url", mgr.URL),
		)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received shutdown signal", zap.String("signal", sig.String()))
		cancel()
	}()

	// Start Prometheus metrics endpoint
	if cfg.Telemetry.Enabled {
		go func() {
			http.Handle("/metrics", promhttp.Handler())
			logger.Info("telemetry listening", zap.String("addr", cfg.Telemetry.Address))
			if err := http.ListenAndServe(cfg.Telemetry.Address, nil); err != nil && err != http.ErrServerClosed {
				logger.Error("telemetry server error", zap.Error(err))
			}
		}()
	}

	startFields := []zap.Field{
		zap.Int("managers", len(managers)),
		zap.String("influxdb", cfg.InfluxDB.URL),
		zap.String("bucket", cfg.InfluxDB.Bucket),
	}
	if cfg.InfluxDB.CapacityBucket != "" {
		startFields = append(startFields, zap.String("capacity_bucket", cfg.InfluxDB.CapacityBucket))
	}
	startFields = append(startFields,
		zap.Duration("interval", cfg.Intervals.Default),
		zap.Duration("slow_interval", cfg.Intervals.Slow),
	)
	logger.Info("nsx-collector starting", startFields...)

	// Start scheduler (blocks until context cancelled)
	sched := collector.NewScheduler(workers, cfg.Intervals.Default)
	if err := sched.Start(ctx); err != nil {
		logger.Fatal("scheduler error", zap.Error(err))
	}

	logger.Info("nsx-collector stopped")
}

// runPrintClusters queries each enabled manager for its T0 edge clusters and
// prints a flat JSON array to stdout, then exits. Used by
// scripts/generate-mrpe-ha.sh to render one MRPE entry per T0 cluster.
//
// Output schema (one element per T0 cluster, sorted by site then name):
//   [{"site":"TESP3","t0_cluster_id":"53129cbd-...","t0_display_name":"T0-Cluster_1"}, ...]
//
// Exit code: 0 if at least one cluster found; 1 if any manager fails or none
// returned. Errors go to stderr; stdout stays clean for piping.
func runPrintClusters(managers []config.Manager) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var out []collector.T0Cluster
	failed := false
	for _, mgr := range managers {
		client := nsx.NewClient(mgr.URL, mgr.Username, mgr.Password, mgr.TLSSkipVerify)
		clusters, err := collector.ListT0Clusters(ctx, client, mgr.Site)
		if err != nil {
			fmt.Fprintf(os.Stderr, "print-clusters: %s: %v\n", mgr.Site, err)
			failed = true
			continue
		}
		out = append(out, clusters...)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "print-clusters: encode: %v\n", err)
		os.Exit(1)
	}
	if failed || len(out) == 0 {
		os.Exit(1)
	}
}

func initLogger(cfg config.LoggingConfig) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		level = zapcore.InfoLevel
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder
	if cfg.Format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderCfg)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderCfg)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)
	return zap.New(core, zap.AddCaller()), nil
}
