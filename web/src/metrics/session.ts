// SPDX-License-Identifier: AGPL-3.0-only

export type MetricsConnectionStatus = "connecting" | "live" | "reconnecting" | "unavailable";

export interface MetricsLimits {
  vcpus: number;
  memoryBytes: number;
  diskBytes: number;
}

export interface MetricsSample {
  at: string;
  cpuPercent?: number;
  memoryBytes: number;
  diskBytes: number;
  netRxBps?: number;
  netTxBps?: number;
}

export interface MetricsSnapshot {
  intervalSeconds: number;
  limits: MetricsLimits;
  samples: MetricsSample[];
}

export interface MetricsSessionOptions {
  instanceId: string;
  csrfToken: string;
  WebSocketImpl?: typeof WebSocket;
  location?: Pick<Location, "protocol" | "host">;
  onStatus?: (status: MetricsConnectionStatus, detail?: string) => void;
  onSnapshot?: (snapshot: MetricsSnapshot) => void;
  onSample?: (sample: MetricsSample) => void;
}

export function metricsWebSocketUrl(
  instanceId: string,
  csrfToken: string,
  location: Pick<Location, "protocol" | "host"> = window.location,
): string {
  const protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const params = new URLSearchParams({ csrf: csrfToken });
  return `${protocol}//${location.host}/v1/instances/${encodeURIComponent(instanceId)}/metrics?${params}`;
}

export class MetricsSession {
  private readonly options: MetricsSessionOptions;
  private socket: WebSocket | null = null;
  private intentionalClose = false;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private attempt = 0;

  constructor(options: MetricsSessionOptions) {
    this.options = options;
  }

  connect(): void {
    this.intentionalClose = false;
    this.openSocket();
  }

  close(): void {
    this.intentionalClose = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    this.disposeSocket();
    this.options.onStatus?.("unavailable");
  }

  private openSocket(): void {
    this.disposeSocket();
    this.options.onStatus?.(this.attempt === 0 ? "connecting" : "reconnecting");
    const WebSocketImpl = this.options.WebSocketImpl ?? WebSocket;
    const location = this.options.location ?? window.location;
    const socket = new WebSocketImpl(
      metricsWebSocketUrl(this.options.instanceId, this.options.csrfToken, location),
    );
    this.socket = socket;
    socket.addEventListener("open", () => {
      this.attempt = 0;
      this.options.onStatus?.("live");
    });
    socket.addEventListener("message", (event) => {
      this.handleMessage(String(event.data));
    });
    socket.addEventListener("close", () => {
      this.socket = null;
      if (this.intentionalClose) return;
      this.scheduleReconnect();
    });
    socket.addEventListener("error", () => {
      this.options.onStatus?.("unavailable", "connection error");
    });
  }

  private scheduleReconnect(): void {
    this.attempt += 1;
    const delay = Math.min(8_000, 500 * 2 ** Math.min(this.attempt, 4));
    this.options.onStatus?.("reconnecting");
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.intentionalClose) this.openSocket();
    }, delay);
  }

  private disposeSocket(): void {
    if (!this.socket) return;
    this.socket.close();
    this.socket = null;
  }

  private handleMessage(raw: string): void {
    let frame: Record<string, unknown>;
    try {
      frame = JSON.parse(raw) as Record<string, unknown>;
    } catch {
      return;
    }
    const type = typeof frame.type === "string" ? frame.type : "";
    if (type === "snapshot") {
      this.options.onSnapshot?.(normalizeSnapshot(frame));
      return;
    }
    if (type === "sample") {
      const sample = asRecord(frame.sample);
      if (sample) this.options.onSample?.(normalizeSample(sample));
      return;
    }
    if (type === "error") {
      const message = typeof frame.message === "string" ? frame.message : "metrics unavailable";
      this.options.onStatus?.("unavailable", message);
    }
  }
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return typeof value === "object" && value !== null ? value as Record<string, unknown> : null;
}

function numberField(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function normalizeSample(row: Record<string, unknown>): MetricsSample {
  return {
    at: typeof row.t === "string" ? row.t : "",
    cpuPercent: numberField(row.cpu_percent),
    memoryBytes: numberField(row.memory_bytes) ?? 0,
    diskBytes: numberField(row.disk_bytes) ?? 0,
    netRxBps: numberField(row.net_rx_bps),
    netTxBps: numberField(row.net_tx_bps),
  };
}

function normalizeSnapshot(frame: Record<string, unknown>): MetricsSnapshot {
  const limits = asRecord(frame.limits) ?? {};
  const samples = Array.isArray(frame.samples)
    ? frame.samples.map((row) => normalizeSample(asRecord(row) ?? {}))
    : [];
  return {
    intervalSeconds: numberField(frame.interval_seconds) ?? 10,
    limits: {
      vcpus: numberField(limits.vcpus) ?? 0,
      memoryBytes: numberField(limits.memory_bytes) ?? 0,
      diskBytes: numberField(limits.disk_bytes) ?? 0,
    },
    samples,
  };
}
