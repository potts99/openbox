// SPDX-License-Identifier: AGPL-3.0-only

export interface BootstrapStatus { required: boolean }
export interface Owner { displayName: string }
export type Session = { authenticated: false } | { authenticated: true; owner: Owner };

export interface Capabilities {
  architecture: string;
  containers: boolean;
  virtualMachines: boolean;
  vmAvailability: string;
  vmReason?: string;
}

export interface InstanceSummary {
  id: string;
  name: string;
  kind: string;
  status: string;
}

export interface OperationSummary {
  id: string;
  action: string;
  status: string;
  target: string;
  updatedAt: string;
}

export interface OpenBoxApi {
  getBootstrapStatus(): Promise<BootstrapStatus>;
  getSession(): Promise<Session>;
  getCapabilities(): Promise<Capabilities>;
  listInstances(): Promise<InstanceSummary[]>;
  listOperations(): Promise<OperationSummary[]>;
  setup(input: { secret: string; password: string }): Promise<Session>;
  login(input: { password: string }): Promise<Session>;
  logout(): Promise<void>;
}

interface HttpApiOptions {
  fetcher?: typeof fetch;
  csrfToken?: string;
}

type JsonRecord = Record<string, unknown>;

const safeErrors: Record<string, string> = {
  unauthenticated: "Invalid credentials",
  bootstrap_unavailable: "Setup is unavailable. Restart openboxd to issue a new one-time secret.",
  insecure_transport: "Setup and login require a local or encrypted connection.",
  forbidden: "This session is not allowed to perform that action.",
  invalid_argument: "Check the submitted values and try again.",
  rate_limited: "Too many attempts. Wait a moment and try again.",
};

function asRecord(value: unknown): JsonRecord {
  return typeof value === "object" && value !== null ? value as JsonRecord : {};
}

function text(value: unknown, fallback = ""): string {
  return typeof value === "string" ? value : fallback;
}

function bool(value: unknown): boolean {
  return value === true;
}

function normalizeSession(value: unknown): Session {
  const result = asRecord(value);
  const authenticated = bool(result.authenticated) || text(result.owner_id) !== "";
  if (!authenticated) return { authenticated: false };
  const owner = asRecord(result.owner);
  return {
    authenticated: true,
    owner: { displayName: text(owner.display_name ?? owner.displayName ?? result.owner_name, "Owner") },
  };
}

export function createHttpApi(options: HttpApiOptions = {}): OpenBoxApi {
  const fetcher = options.fetcher ?? fetch;
  let csrfToken = options.csrfToken ?? "";

  async function request(path: string, init: RequestInit = {}, allowedStatuses: number[] = []): Promise<unknown> {
    const method = init.method ?? "GET";
    const headers: Record<string, string> = { "X-OpenBox-API-Version": "v1" };
    if (init.body !== undefined) headers["Content-Type"] = "application/json";
    if (!/^(GET|HEAD|OPTIONS)$/u.test(method) && csrfToken) headers["X-CSRF-Token"] = csrfToken;

    const response = await fetcher(path, {
      ...init,
      credentials: "same-origin",
      headers: { ...headers, ...init.headers },
    });
    const nextToken = response.headers.get("x-openbox-csrf-token");
    if (nextToken) csrfToken = nextToken;
    const isJson = response.headers.get("content-type")?.includes("application/json") ?? false;
    const body: unknown = isJson ? await response.json() : undefined;
    const bodyToken = text(asRecord(body).csrf_token);
    if (bodyToken) csrfToken = bodyToken;
    if (allowedStatuses.includes(response.status)) return body;
    if (!response.ok) {
      const error = asRecord(asRecord(body).error);
      throw new Error(safeErrors[text(error.code)] ?? "OpenBox rejected the request");
    }
    return body;
  }

  return {
    async getBootstrapStatus() {
      const body = asRecord(await request("/v1/bootstrap"));
      return { required: bool(body.required ?? body.bootstrap_required) };
    },
    async getSession() {
      return normalizeSession(await request("/v1/session", {}, [401]));
    },
    async getCapabilities() {
      const body = asRecord(await request("/v1/capabilities"));
      return {
        architecture: text(body.architecture, "unknown"),
        containers: bool(body.containers),
        virtualMachines: bool(body.virtual_machines),
        vmAvailability: text(body.vm_availability, "unknown"),
        vmReason: text(body.vm_reason) || undefined,
      };
    },
    async listInstances() {
      const body = asRecord(await request("/v1/instances"));
      const items = Array.isArray(body.items) ? body.items : Array.isArray(body.instances) ? body.instances : [];
      return items.map((item): InstanceSummary => {
        const row = asRecord(item);
        return { id: text(row.id), name: text(row.name), kind: text(row.kind), status: text(row.observed_state) };
      });
    },
    async listOperations() {
      const body = asRecord(await request("/v1/operations"));
      const items = Array.isArray(body.items) ? body.items : Array.isArray(body.operations) ? body.operations : [];
      return items.map((item): OperationSummary => {
        const row = asRecord(item);
        return {
          id: text(row.id),
          action: text(row.type),
          status: text(row.status),
          target: text(row.target_id, "system"),
          updatedAt: text(row.updated_at),
        };
      });
    },
    async setup(input) {
      return normalizeSession(await request("/v1/bootstrap", { method: "POST", body: JSON.stringify(input) }));
    },
    async login(input) {
      return normalizeSession(await request("/v1/sessions", { method: "POST", body: JSON.stringify(input) }));
    },
    async logout() {
      await request("/v1/session", { method: "DELETE" });
    },
  };
}
