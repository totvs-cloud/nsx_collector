package influxdb

import (
	"context"
	"fmt"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/influxdata/influxdb-client-go/v2/api/write"
	"go.uber.org/zap"
)

// Writer wraps the InfluxDB non-blocking write API.
type Writer struct {
	writeAPI api.WriteAPIBlocking
	logger   *zap.Logger
}

// NewWriter creates a new InfluxDB writer.
func NewWriter(client influxdb2.Client, org, bucket string) *Writer {
	return &Writer{
		writeAPI: client.WriteAPIBlocking(org, bucket),
		logger:   zap.L().Named("influxdb"),
	}
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
