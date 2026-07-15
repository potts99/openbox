// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useState } from "react";
import {
  MetricsSession,
  type MetricsConnectionStatus,
  type MetricsLimits,
  type MetricsSample,
} from "../metrics/session";

interface InstanceMetricsProps {
  instanceId: string;
  csrfToken: string;
  vcpus: number;
  memoryBytes: number;
  diskBytes: number;
  WebSocketImpl?: typeof WebSocket;
}

const maxPoints = 360;

export function InstanceMetrics({
  instanceId,
  csrfToken,
  vcpus,
  memoryBytes,
  diskBytes,
  WebSocketImpl,
}: InstanceMetricsProps) {
  const fallbackLimits: MetricsLimits = { vcpus, memoryBytes, diskBytes };
  const [status, setStatus] = useState<MetricsConnectionStatus>("connecting");
  const [limits, setLimits] = useState<MetricsLimits>(fallbackLimits);
  const [samples, setSamples] = useState<MetricsSample[]>([]);

  useEffect(() => {
    const session = new MetricsSession({
      instanceId,
      csrfToken,
      WebSocketImpl,
      onStatus: setStatus,
      onSnapshot: (snapshot) => {
        const next = snapshot.limits;
        setLimits(next.vcpus || next.memoryBytes || next.diskBytes ? next : {
          vcpus, memoryBytes, diskBytes,
        });
        setSamples(snapshot.samples.slice(-maxPoints));
      },
      onSample: (sample) => {
        setSamples((prev) => [...prev, sample].slice(-maxPoints));
      },
    });
    session.connect();
    return () => session.close();
  }, [WebSocketImpl, csrfToken, diskBytes, instanceId, memoryBytes, vcpus]);

  const latest = samples[samples.length - 1];
  const cpuSeries = samples.map((s) => s.cpuPercent ?? null);
  const memSeries = samples.map((s) => percentOf(s.memoryBytes, limits.memoryBytes));
  const diskSeries = samples.map((s) => percentOf(s.diskBytes, limits.diskBytes));
  const netSeries = samples.map((s) => {
    const rx = s.netRxBps ?? 0;
    const tx = s.netTxBps ?? 0;
    return rx + tx > 0 ? rx + tx : null;
  });

  return (
    <section className="instance-detail instance-metrics" aria-labelledby="instance-metrics-heading">
      <div className="ledger-header">
        <h2 id="instance-metrics-heading">Monitoring</h2>
        <span className={`metrics-status metrics-status-${status}`}>{statusLabel(status)}</span>
      </div>
      <dl className="metrics-readouts" aria-label="Live usage">
        <div>
          <dt>CPU</dt>
          <dd>{formatCPU(latest?.cpuPercent)}</dd>
        </div>
        <div>
          <dt>Memory</dt>
          <dd>{formatUsedLimit(latest?.memoryBytes, limits.memoryBytes)}</dd>
        </div>
        <div>
          <dt>Disk</dt>
          <dd>{formatUsedLimit(latest?.diskBytes, limits.diskBytes)}</dd>
        </div>
        <div>
          <dt>Net</dt>
          <dd>{formatNet(latest?.netRxBps, latest?.netTxBps)}</dd>
        </div>
      </dl>
      <div className="metrics-charts" aria-label="Usage history, last 60 minutes">
        <Sparkline title="CPU" values={cpuSeries} unit="%" />
        <Sparkline title="Memory" values={memSeries} unit="%" />
        <Sparkline title="Disk" values={diskSeries} unit="%" />
        <Sparkline title="Net" values={netSeries} unit="B/s" />
      </div>
      <p className="metrics-caption">Last 60 minutes</p>
    </section>
  );
}

function Sparkline({ title, values, unit }: { title: string; values: Array<number | null>; unit: string }) {
  const width = 160;
  const height = 36;
  const path = sparkPath(values, width, height);
  return (
    <figure className="metrics-spark">
      <figcaption>{title}</figcaption>
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${title} sparkline (${unit})`}>
        <path d={path} fill="none" stroke="currentColor" strokeWidth="1.5" />
      </svg>
    </figure>
  );
}

function sparkPath(values: Array<number | null>, width: number, height: number): string {
  const points = values
    .map((value, index) => ({ value, index }))
    .filter((point): point is { value: number; index: number } => point.value !== null && Number.isFinite(point.value));
  if (points.length === 0) return `M0 ${height / 2}`;
  const max = Math.max(...points.map((p) => p.value), 1);
  const min = Math.min(...points.map((p) => p.value), 0);
  const span = Math.max(max - min, 1e-9);
  const n = Math.max(values.length - 1, 1);
  return points.map((point, i) => {
    const x = (point.index / n) * width;
    const y = height - ((point.value - min) / span) * (height - 4) - 2;
    return `${i === 0 ? "M" : "L"}${x.toFixed(2)} ${y.toFixed(2)}`;
  }).join(" ");
}

function percentOf(used: number, limit: number): number | null {
  if (!limit || used < 0) return null;
  return Math.min(100, (used / limit) * 100);
}

function formatCPU(value?: number): string {
  if (value === undefined) return "—";
  const rounded = Math.round(value * 10) / 10;
  return `${Number.isInteger(rounded) ? rounded.toFixed(0) : rounded.toFixed(1)}%`;
}

function formatUsedLimit(used: number | undefined, limit: number): string {
  if (used === undefined) return "—";
  return `${formatBytes(used)} / ${formatBytes(limit)}`;
}

function formatNet(rx?: number, tx?: number): string {
  if (rx === undefined && tx === undefined) return "—";
  return `↓ ${formatRate(rx ?? 0)} ↑ ${formatRate(tx ?? 0)}`;
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const gib = bytes / (1024 ** 3);
  if (gib >= 1) return `${gib % 1 === 0 ? gib.toFixed(0) : gib.toFixed(1)} GiB`;
  const mib = bytes / (1024 ** 2);
  if (mib >= 1) return `${mib % 1 === 0 ? mib.toFixed(0) : mib.toFixed(1)} MiB`;
  const kib = bytes / 1024;
  if (kib >= 1) return `${kib.toFixed(0)} KiB`;
  return `${bytes} B`;
}

function formatRate(bps: number): string {
  if (bps < 1024) return `${bps.toFixed(0)} B/s`;
  if (bps < 1024 ** 2) return `${(bps / 1024).toFixed(1)} KiB/s`;
  return `${(bps / (1024 ** 2)).toFixed(1)} MiB/s`;
}

function statusLabel(status: MetricsConnectionStatus): string {
  switch (status) {
    case "connecting":
      return "connecting";
    case "live":
      return "live";
    case "reconnecting":
      return "reconnecting";
    case "unavailable":
      return "unavailable";
    default: {
      const _exhaustive: never = status;
      return _exhaustive;
    }
  }
}
