// SPDX-License-Identifier: AGPL-3.0-only

export type OperationStreamStatus = "connecting" | "live" | "complete" | "error" | "closed";

export interface OperationEvent {
  sequence: number;
  operationId: string;
  stage: string;
  status: string;
  progress: number;
  errorClass?: string;
  errorCode?: string;
  message?: string;
  createdAt: string;
}

export interface OperationEventsSessionOptions {
  operationId: string;
  EventSourceImpl?: typeof EventSource;
  onStatus?: (status: OperationStreamStatus, detail?: string) => void;
  onEvent?: (event: OperationEvent) => void;
  onError?: (detail: string) => void;
}

export function operationEventsUrl(operationId: string): string {
  return `/v1/operations/${encodeURIComponent(operationId)}/events`;
}

export class OperationEventsSession {
  private readonly options: OperationEventsSessionOptions;
  private source: EventSource | null = null;
  private intentionalClose = false;
  private receivedTerminal = false;

  constructor(options: OperationEventsSessionOptions) {
    this.options = options;
  }

  connect(): void {
    this.intentionalClose = false;
    this.receivedTerminal = false;
    this.options.onStatus?.("connecting");
    const EventSourceImpl = this.options.EventSourceImpl ?? EventSource;
    const source = new EventSourceImpl(operationEventsUrl(this.options.operationId));
    this.source = source;

    source.addEventListener("operation", (event) => {
      const message = event as MessageEvent<string>;
      const parsed = parseOperationEvent(message.data);
      if (!parsed) return;
      this.options.onStatus?.("live");
      this.options.onEvent?.(parsed);
      if (isTerminalStatus(parsed.status)) {
        this.receivedTerminal = true;
        this.options.onStatus?.("complete");
        this.close(false);
      }
    });

    source.addEventListener("error", (event) => {
      if (this.intentionalClose || this.receivedTerminal) return;
      const message = event as MessageEvent<string>;
      const detail = parseStreamError(message.data);
      this.options.onStatus?.("error", detail);
      this.options.onError?.(detail);
    });

    source.onerror = () => {
      if (this.intentionalClose || this.receivedTerminal) return;
      this.options.onStatus?.("error", "Event stream interrupted");
      this.options.onError?.("Event stream interrupted");
    };
  }

  close(notify = true): void {
    this.intentionalClose = true;
    if (this.source) {
      this.source.close();
      this.source = null;
    }
    if (notify) this.options.onStatus?.("closed");
  }
}

function parseOperationEvent(raw: string): OperationEvent | null {
  let value: Record<string, unknown>;
  try {
    value = JSON.parse(raw) as Record<string, unknown>;
  } catch {
    return null;
  }
  const sequence = typeof value.sequence === "number" ? value.sequence : 0;
  if (sequence <= 0) return null;
  return {
    sequence,
    operationId: typeof value.operation_id === "string" ? value.operation_id : "",
    stage: typeof value.stage === "string" ? value.stage : "",
    status: typeof value.status === "string" ? value.status : "",
    progress: typeof value.progress === "number" && Number.isFinite(value.progress) ? value.progress : 0,
    errorClass: typeof value.error_class === "string" ? value.error_class : undefined,
    errorCode: typeof value.error_code === "string" ? value.error_code : undefined,
    message: typeof value.message === "string" ? value.message : undefined,
    createdAt: typeof value.created_at === "string" ? value.created_at : "",
  };
}

function parseStreamError(raw: string): string {
  try {
    const value = JSON.parse(raw) as Record<string, unknown>;
    const error = typeof value.error === "object" && value.error !== null
      ? value.error as Record<string, unknown>
      : value;
    const message = typeof error.message === "string" ? error.message : "";
    if (message) return message;
  } catch {
    // fall through
  }
  return "Event stream could not be continued";
}

function isTerminalStatus(status: string): boolean {
  return status === "succeeded" || status === "failed";
}
