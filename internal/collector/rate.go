package collector

import (
	"sync"
	"time"
)

type counterState struct {
	value uint64
	ts    time.Time
}

// RateResult holds the calculated rate for a single interface.
type RateResult struct {
	RxBps           float64
	TxBps           float64
	RxUtilizationPct float64
	TxUtilizationPct float64
	LinkSpeedMbps   int64
}

// RateCalculator computes per-interface bandwidth rates from cumulative byte counters.
// It stores the previous counter values in memory and calculates the delta on each call.
type RateCalculator struct {
	mu    sync.Mutex
	state map[string]counterState // key: "node:iface:rx" or "node:iface:tx"
}

func NewRateCalculator() *RateCalculator {
	return &RateCalculator{
		state: make(map[string]counterState),
	}
}

// Calculate computes rx/tx rates in bits per second from cumulative byte counters.
// Returns nil on the first sample for a given interface (needs two readings).
// Handles counter wraps for uint64 and discards samples where the computed rate
// exceeds a sanity threshold (counter reset).
func (rc *RateCalculator) Calculate(nodeName, ifaceID string, rxBytes, txBytes uint64, linkSpeedMbps int64, now time.Time) *RateResult {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	rxKey := nodeName + ":" + ifaceID + ":rx"
	txKey := nodeName + ":" + ifaceID + ":tx"

	prevRx, hasRx := rc.state[rxKey]
	prevTx, hasTx := rc.state[txKey]

	rc.state[rxKey] = counterState{value: rxBytes, ts: now}
	rc.state[txKey] = counterState{value: txBytes, ts: now}

	if !hasRx || !hasTx {
		return nil
	}

	elapsed := now.Sub(prevRx.ts).Seconds()
	if elapsed <= 0 {
		return nil
	}

	rxBytesPerSec := counterRate(prevRx.value, rxBytes, elapsed)
	txBytesPerSec := counterRate(prevTx.value, txBytes, elapsed)

	if rxBytesPerSec < 0 || txBytesPerSec < 0 {
		return nil
	}

	// Sanity check: discard if rate exceeds 100 Gbps (likely counter reset)
	const maxBytesPerSec = 100_000_000_000 / 8
	if rxBytesPerSec > maxBytesPerSec || txBytesPerSec > maxBytesPerSec {
		return nil
	}

	rxBps := rxBytesPerSec * 8
	txBps := txBytesPerSec * 8

	result := &RateResult{
		RxBps:         rxBps,
		TxBps:         txBps,
		LinkSpeedMbps: linkSpeedMbps,
	}

	if linkSpeedMbps > 0 {
		linkBps := float64(linkSpeedMbps) * 1_000_000
		result.RxUtilizationPct = min((rxBps/linkBps)*100, 100)
		result.TxUtilizationPct = min((txBps/linkBps)*100, 100)
	}

	return result
}

// counterRate calculates bytes/sec handling uint64 counter wrap.
func counterRate(prev, curr uint64, elapsedSec float64) float64 {
	var delta uint64
	if curr >= prev {
		delta = curr - prev
	} else {
		// Counter wrapped around uint64 max
		delta = (^uint64(0) - prev) + curr + 1
	}
	return float64(delta) / elapsedSec
}
