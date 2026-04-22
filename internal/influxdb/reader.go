package influxdb

import (
	"context"
	"fmt"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"go.uber.org/zap"
)

// Reader wraps the InfluxDB query API to fetch aggregated data the same way
// Grafana panels do, so alerts are based on identical numbers to what the
// dashboards display.
type Reader struct {
	queryAPI api.QueryAPI
	bucket   string
	logger   *zap.Logger
}

// NewReader builds a Reader bound to the same org/bucket used by Grafana.
func NewReader(client influxdb2.Client, org, bucket string) *Reader {
	return &Reader{
		queryAPI: client.QueryAPI(org),
		bucket:   bucket,
		logger:   zap.L().Named("influxdb-reader"),
	}
}

// EdgeUtilAvg returns the mean rx_utilization_pct and tx_utilization_pct for
// (site, node, iface) over the last `window`, using the same aggregateWindow
// granularity the Grafana panels use (2 minute buckets, mean).
//
// Flux (RX, TX mirrors it):
//   from(bucket: "nsx")
//     |> range(start: -<window>)
//     |> filter(_measurement == "nsx_edge_bandwidth")
//     |> filter(_field == "rx_utilization_pct")
//     |> filter(site == <site>, node_name == <node>, interface_id == <iface>)
//     |> aggregateWindow(every: 2m, fn: mean, createEmpty: false)
//     |> mean()
func (r *Reader) EdgeUtilAvg(ctx context.Context, site, nodeName, ifaceID, window string) (rxAvg, txAvg float64, err error) {
	query := fmt.Sprintf(`
data = from(bucket: "%s")
  |> range(start: -%s)
  |> filter(fn: (r) => r._measurement == "nsx_edge_bandwidth")
  |> filter(fn: (r) => r.site == "%s")
  |> filter(fn: (r) => r.node_name == "%s")
  |> filter(fn: (r) => r.interface_id == "%s")

rx = data
  |> filter(fn: (r) => r._field == "rx_utilization_pct")
  |> aggregateWindow(every: 2m, fn: mean, createEmpty: false)
  |> mean()
  |> set(key: "dir", value: "rx")

tx = data
  |> filter(fn: (r) => r._field == "tx_utilization_pct")
  |> aggregateWindow(every: 2m, fn: mean, createEmpty: false)
  |> mean()
  |> set(key: "dir", value: "tx")

union(tables: [rx, tx]) |> keep(columns: ["dir", "_value"])
`, r.bucket, window, site, nodeName, ifaceID)

	result, err := r.queryAPI.Query(ctx, query)
	if err != nil {
		return 0, 0, fmt.Errorf("flux query: %w", err)
	}
	defer result.Close()

	for result.Next() {
		rec := result.Record()
		v, ok := rec.Value().(float64)
		if !ok {
			continue
		}
		switch rec.ValueByKey("dir") {
		case "rx":
			rxAvg = v
		case "tx":
			txAvg = v
		}
	}
	if result.Err() != nil {
		return 0, 0, fmt.Errorf("flux result: %w", result.Err())
	}
	return rxAvg, txAvg, nil
}
