// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/openbox-dev/openbox/internal/app/metrics"
	"github.com/openbox-dev/openbox/internal/domain"
)

type metricsFrame struct {
	Type            string          `json:"type"`
	IntervalSeconds int             `json:"interval_seconds,omitempty"`
	Limits          *metricsLimits  `json:"limits,omitempty"`
	Samples         []metricsSample `json:"samples,omitempty"`
	Sample          *metricsSample  `json:"sample,omitempty"`
	Code            string          `json:"code,omitempty"`
	Message         string          `json:"message,omitempty"`
}

type metricsLimits struct {
	VCPUs       int   `json:"vcpus"`
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
}

type metricsSample struct {
	At          time.Time `json:"t"`
	CPUPercent  *float64  `json:"cpu_percent,omitempty"`
	MemoryBytes int64     `json:"memory_bytes"`
	DiskBytes   int64     `json:"disk_bytes"`
	NetRxBps    *float64  `json:"net_rx_bps,omitempty"`
	NetTxBps    *float64  `json:"net_tx_bps,omitempty"`
}

func (h *Handler) openMetrics(response http.ResponseWriter, request *http.Request, requestID, rawID string) {
	if h.metrics == nil {
		h.writeError(response, requestID, http.StatusNotImplemented, "not_implemented", "metrics")
		return
	}
	if !terminalAuthUsesBearer(request) && !terminalOriginAllowed(request) {
		h.writeError(response, requestID, http.StatusForbidden, "forbidden", "origin")
		return
	}

	instance, err := h.service.GetInstance(request.Context(), h.requestOwner(request), domain.InstanceID(rawID))
	if err != nil {
		h.writeServiceError(response, requestID, err)
		return
	}

	conn, err := websocket.Accept(response, request, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := request.Context()
	if err := h.writeMetricsSnapshot(ctx, conn, instance); err != nil {
		return
	}

	samples := h.metrics.Subscribe(instance.ID)
	defer h.metrics.Unsubscribe(instance.ID, samples)

	for {
		select {
		case <-ctx.Done():
			return
		case sample, ok := <-samples:
			if !ok {
				return
			}
			if err := h.writeMetricsSample(ctx, conn, sample); err != nil {
				return
			}
		}
	}
}

func (h *Handler) writeMetricsSnapshot(ctx context.Context, conn *websocket.Conn, instance domain.Instance) error {
	snap := h.metrics.Snapshot(instance.ID)
	limits := snap.Limits
	if limits.VCPUs == 0 && limits.MemoryBytes == 0 && limits.DiskBytes == 0 {
		limits = metrics.Limits{
			VCPUs:       instance.Resources.VCPUs,
			MemoryBytes: instance.Resources.MemoryBytes,
			DiskBytes:   instance.Resources.DiskBytes,
		}
	}
	frame := metricsFrame{
		Type:            "snapshot",
		IntervalSeconds: snap.IntervalSeconds,
		Limits: &metricsLimits{
			VCPUs:       limits.VCPUs,
			MemoryBytes: limits.MemoryBytes,
			DiskBytes:   limits.DiskBytes,
		},
		Samples: encodeMetricsSamples(snap.Samples),
	}
	return writeMetricsFrame(ctx, conn, frame)
}

func (h *Handler) writeMetricsSample(ctx context.Context, conn *websocket.Conn, sample metrics.Sample) error {
	encoded := encodeMetricsSample(sample)
	return writeMetricsFrame(ctx, conn, metricsFrame{Type: "sample", Sample: &encoded})
}

func writeMetricsFrame(ctx context.Context, conn *websocket.Conn, frame metricsFrame) error {
	payload, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, payload)
}

func encodeMetricsSamples(samples []metrics.Sample) []metricsSample {
	out := make([]metricsSample, 0, len(samples))
	for _, sample := range samples {
		out = append(out, encodeMetricsSample(sample))
	}
	return out
}

func encodeMetricsSample(sample metrics.Sample) metricsSample {
	return metricsSample{
		At:          sample.At.UTC(),
		CPUPercent:  sample.CPUPercent,
		MemoryBytes: sample.MemoryBytes,
		DiskBytes:   sample.DiskBytes,
		NetRxBps:    sample.NetRxBps,
		NetTxBps:    sample.NetTxBps,
	}
}
