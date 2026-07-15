// SPDX-License-Identifier: AGPL-3.0-only

export interface BootstrapStatus { required: boolean }
export interface Owner { displayName: string }
export type Session =
  | { authenticated: false }
  | { authenticated: true; owner: Owner; csrfToken: string };

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

export interface NetworkPolicyStatus {
  egressMode: string;
  acls: string[];
  resolutionState: string;
  deniedFlows: number;
}

export interface InstanceSoftware {
  packageId: string;
  status: string;
  version?: string;
  error?: string;
  updatedAt: string;
}

export interface SoftwarePackage {
  id: string;
  name: string;
  description: string;
}

export interface InstanceDetail {
  id: string;
  name: string;
  kind: string;
  imageId: string;
  requestedIsolation: string;
  actualIsolation: string;
  desiredState: string;
  observedState: string;
  vcpus: number;
  memoryBytes: number;
  diskBytes: number;
  protected: boolean;
  createdAt: string;
  updatedAt: string;
  expiresAt?: string;
  errorCode?: string;
  errorStage?: string;
  networkPolicy: NetworkPolicyStatus;
  software: InstanceSoftware[];
}

export interface OperationSummary {
  id: string;
  action: string;
  status: string;
  target: string;
  updatedAt: string;
}

export type InstanceAction = "start" | "stop" | "restart";

export interface PiProfileSummary {
  id: string;
  name: string;
  version: number;
  settingsJson: string;
  updatedAt: string;
}

export interface PiProfileVersion {
  version: number;
  settingsJson: string;
  createdAt: string;
}

export interface OpenBoxApi {
  getBootstrapStatus(): Promise<BootstrapStatus>;
  getSession(): Promise<Session>;
  getCsrfToken(): string;
  getCapabilities(): Promise<Capabilities>;
  listInstances(): Promise<InstanceSummary[]>;
  getInstance(id: string): Promise<InstanceDetail>;
  listSoftwareCatalog(): Promise<SoftwarePackage[]>;
  installSoftware(instanceId: string, packageId: string): Promise<InstanceSoftware>;
  mutateInstance(id: string, action: InstanceAction): Promise<OperationSummary>;
  listOperations(): Promise<OperationSummary[]>;
  listPiProfiles(): Promise<PiProfileSummary[]>;
  getPiProfileHistory(id: string): Promise<PiProfileVersion[]>;
  rollbackPiProfile(id: string, version: number): Promise<PiProfileSummary>;
  applyPiProfile(id: string, instanceIds: string[]): Promise<void>;
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

function number(value: unknown, fallback = 0): number {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function normalizeInstanceSoftware(value: unknown): InstanceSoftware {
  const row = asRecord(value);
  return {
    packageId: text(row.package_id),
    status: text(row.status),
    version: text(row.version) || undefined,
    error: text(row.error) || undefined,
    updatedAt: text(row.updated_at),
  };
}

function normalizeInstance(value: unknown): InstanceDetail {
  const row = asRecord(value);
  const resources = asRecord(row.resources);
  const networkPolicy = asRecord(row.network_policy);
  const resolution = asRecord(networkPolicy.resolution);
  const softwareRaw = Array.isArray(row.software) ? row.software : [];
  return {
    id: text(row.id),
    name: text(row.name),
    kind: text(row.kind),
    imageId: text(row.image_id),
    requestedIsolation: text(row.requested_isolation),
    actualIsolation: text(row.actual_isolation),
    desiredState: text(row.desired_state),
    observedState: text(row.observed_state),
    vcpus: number(resources.vcpus),
    memoryBytes: number(resources.memory_bytes),
    diskBytes: number(resources.disk_bytes),
    protected: bool(row.protected),
    createdAt: text(row.created_at),
    updatedAt: text(row.updated_at),
    expiresAt: text(row.expires_at) || undefined,
    errorCode: text(row.error_code) || undefined,
    errorStage: text(row.error_stage) || undefined,
    networkPolicy: {
      egressMode: text(networkPolicy.egress_mode),
      acls: Array.isArray(networkPolicy.acls) ? networkPolicy.acls.filter((acl): acl is string => typeof acl === "string") : [],
      resolutionState: text(resolution.state),
      deniedFlows: number(networkPolicy.denied_flows),
    },
    software: softwareRaw.map(normalizeInstanceSoftware),
  };
}

function normalizeOperation(value: unknown): OperationSummary {
  const row = asRecord(value);
  return {
    id: text(row.id),
    action: text(row.type),
    status: text(row.status),
    target: text(row.target_id, "system"),
    updatedAt: text(row.updated_at),
  };
}

function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

function normalizeSession(value: unknown, fallbackCsrf = ""): Session {
  const result = asRecord(value);
  const authenticated = bool(result.authenticated) || text(result.owner_id) !== "";
  if (!authenticated) return { authenticated: false };
  const owner = asRecord(result.owner);
  return {
    authenticated: true,
    owner: { displayName: text(owner.display_name ?? owner.displayName ?? result.owner_name, "Owner") },
    csrfToken: text(result.csrf_token, fallbackCsrf),
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
      return normalizeSession(await request("/v1/session", {}, [401]), csrfToken);
    },
    getCsrfToken() {
      return csrfToken;
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
    async getInstance(id) {
      return normalizeInstance(await request(`/v1/instances/${encodeURIComponent(id)}`));
    },
    async listSoftwareCatalog() {
      const body = asRecord(await request("/v1/software"));
      const items = Array.isArray(body.items) ? body.items : [];
      return items.map((item): SoftwarePackage => {
        const row = asRecord(item);
        return {
          id: text(row.id),
          name: text(row.name),
          description: text(row.description),
        };
      });
    },
    async installSoftware(instanceId, packageId) {
      const body = await request(
        `/v1/instances/${encodeURIComponent(instanceId)}/software/${encodeURIComponent(packageId)}/install`,
        { method: "POST" },
      );
      return normalizeInstanceSoftware(body);
    },
    async mutateInstance(id, action) {
      const body = await request(`/v1/instances/${encodeURIComponent(id)}/actions/${action}`, {
        method: "POST",
        headers: { "Idempotency-Key": newIdempotencyKey() },
      });
      return normalizeOperation(body);
    },
    async listOperations() {
      const body = asRecord(await request("/v1/operations"));
      const items = Array.isArray(body.items) ? body.items : Array.isArray(body.operations) ? body.operations : [];
      return items.map((item) => normalizeOperation(item));
    },
    async listPiProfiles() {
      const body = asRecord(await request("/v1/pi-profiles"));
      const items = Array.isArray(body.items) ? body.items : [];
      return items.map((item): PiProfileSummary => {
        const row = asRecord(item);
        return {
          id: text(row.id),
          name: text(row.name),
          version: number(row.version),
          settingsJson: typeof row.settings_json === "string" ? row.settings_json : JSON.stringify(row.settings ?? {}),
          updatedAt: text(row.updated_at),
        };
      });
    },
    async getPiProfileHistory(id) {
      const body = asRecord(await request(`/v1/pi-profiles/${encodeURIComponent(id)}/versions`));
      const items = Array.isArray(body.items) ? body.items : [];
      return items.map((item): PiProfileVersion => {
        const row = asRecord(item);
        return {
          version: number(row.version),
          settingsJson: typeof row.settings_json === "string" ? row.settings_json : JSON.stringify(row.settings ?? {}),
          createdAt: text(row.created_at),
        };
      });
    },
    async rollbackPiProfile(id, version) {
      const body = asRecord(await request(`/v1/pi-profiles/${encodeURIComponent(id)}/rollback`, {
        method: "POST",
        body: JSON.stringify({ version }),
      }));
      return {
        id: text(body.id),
        name: text(body.name),
        version: number(body.version),
        settingsJson: typeof body.settings_json === "string" ? body.settings_json : JSON.stringify(body.settings ?? {}),
        updatedAt: text(body.updated_at),
      };
    },
    async applyPiProfile(id, instanceIds) {
      await request(`/v1/pi-profiles/${encodeURIComponent(id)}/apply`, {
        method: "POST",
        body: JSON.stringify({ instance_ids: instanceIds }),
      });
    },
    async setup(input) {
      return normalizeSession(await request("/v1/bootstrap", { method: "POST", body: JSON.stringify(input) }), csrfToken);
    },
    async login(input) {
      return normalizeSession(await request("/v1/sessions", { method: "POST", body: JSON.stringify(input) }), csrfToken);
    },
    async logout() {
      await request("/v1/session", { method: "DELETE" });
    },
  };
}
