package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"nsx-collector/internal/collector"
	"nsx-collector/internal/config"
	influxpkg "nsx-collector/internal/influxdb"
)

func main() {
	configFile := flag.String("config", "/home/nsx_collector/configs/config.yaml", "Path to config file")
	managersFile := flag.String("managers", "/home/nsx_collector/configs/managers.yaml", "Path to managers file")
	envFile := flag.String("env-file", "/home/nsx_collector/.env", "Path to .env file")
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

	writer := influxpkg.NewWriter(influxClient, cfg.InfluxDB.Org, cfg.InfluxDB.Bucket)

	// Build workers (one per manager)
	var workers []*collector.Worker
	for _, mgr := range managers {
		workers = append(workers, collector.NewWorker(mgr, writer))
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

	logger.Info("nsx-collector starting",
		zap.Int("managers", len(managers)),
		zap.String("influxdb", cfg.InfluxDB.URL),
		zap.String("bucket", cfg.InfluxDB.Bucket),
		zap.Duration("interval", cfg.Intervals.Default),
	)

	// Start scheduler (blocks until context cancelled)
	sched := collector.NewScheduler(workers, cfg.Intervals.Default)
	if err := sched.Start(ctx); err != nil {
		logger.Fatal("scheduler error", zap.Error(err))
	}

	logger.Info("nsx-collector stopped")
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
