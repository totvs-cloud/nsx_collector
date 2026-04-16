package influxdb

import (
	"context"
	"fmt"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"
)

// Writer wraps the InfluxDB blocking write API.
type Writer struct {
	writeAPI         api.WriteAPIBlocking
	capacityWriteAPI api.WriteAPIBlocking // nil when capacity goes to main bucket
	logger           *zap.Logger
}

// NewWriter creates a new InfluxDB writer.
func NewWriter(client influxdb2.Client, org, bucket, capacityBucket string) *Writer {
	w := &Writer{
		writeAPI: client.WriteAPIBlocking(org, bucket),
		logger:   zap.L().Named("influxdb"),
	}
	if capacityBucket != "" && capacityBucket != bucket {
		w.capacityWriteAPI = client.WriteAPIBlocking(org, capacityBucket)
	}
	return w
}

// WritePoints writes a batch of points to InfluxDB.
func (w *Writer) WritePoints(ctx context.Context, points []*write.Point) error {
	if len(points) == 0 {
		return nil
	}
	if err := w.writeAPI.WritePoint(ctx, points...); err != nil {
		return fmt.Errorf("writing %d points: %w", len(points), err)
	}
	w.logger.Debug("points written", zap.Int("count", len(points)))
	return nil
}

// WriteCapacityPoints writes capacity points to the capacity bucket (or main bucket if not configured).
func (w *Writer) WriteCapacityPoints(ctx context.Context, points []*write.Point) error {
	if len(points) == 0 {
		return nil
	}
	api := w.writeAPI
	if w.capacityWriteAPI != nil {
		api = w.capacityWriteAPI
	}
	if err := api.WritePoint(ctx, points...); err != nil {
		return fmt.Errorf("writing %d capacity points: %w", len(points), err)
	}
	w.logger.Debug("capacity points written", zap.Int("count", len(points)))
	return nil
}
