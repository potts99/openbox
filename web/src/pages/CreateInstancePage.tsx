// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import type {
  Capabilities,
  ConnectionInfo,
  CreateInstanceResult,
  ImageSummary,
  InstanceKind,
  IsolationRequest,
  OpenBoxApi,
  SoftwarePackage,
  SSHKeySummary,
} from "../api/client";
import { SSHConnect } from "../components/SSHConnect";

interface CreateInstancePageProps {
  api: OpenBoxApi;
  onBack(): void;
  onCreated(result: CreateInstanceResult): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; images: ImageSummary[]; keys: SSHKeySummary[]; catalog: SoftwarePackage[]; capabilities: Capabilities }
  | { status: "created"; result: CreateInstanceResult; connection: ConnectionInfo };

interface KindDefaults {
  isolation: IsolationRequest;
  vcpus: number;
  memoryGib: number;
  diskGib: number;
  preferredImage: string;
}

const PASTE_KEY = "__paste__";
const GIB = 1024 ** 3;

function vmsAvailable(capabilities: Capabilities): boolean {
  return capabilities.virtualMachines && capabilities.vmAvailability === "supported";
}

function defaultsForKind(kind: InstanceKind, capabilities?: Capabilities): KindDefaults {
  const isolation: IsolationRequest = capabilities && vmsAvailable(capabilities) ? "strong" : "container";
  if (kind === "sandbox") {
    return {
      isolation,
      vcpus: 2,
      memoryGib: 2,
      diskGib: 10,
      preferredImage: "openbox:sandbox/ubuntu/24.04",
    };
  }
  return {
    isolation,
    vcpus: 2,
    memoryGib: 8,
    diskGib: 20,
    preferredImage: "ubuntu",
  };
}

function isFingerprint(value: string): boolean {
  return /^[a-f0-9]{40,}$/i.test(value);
}

function titleCase(value: string): string {
  return value
    .split(/[\s/_-]+/)
    .filter(Boolean)
    .map((part) => (part.toLowerCase() === "ubuntu" ? "Ubuntu" : part.charAt(0).toUpperCase() + part.slice(1)))
    .join(" ");
}

/** Mutable create reference (alias), preferring human source over digest aliases. */
export function imageReference(image: ImageSummary): string {
  const fromSource = image.source.replace(/^incus:/u, "").trim();
  if (fromSource && !isFingerprint(fromSource)) return fromSource;
  if (image.alias && !isFingerprint(image.alias)) return image.alias;
  return image.id || image.alias;
}

export function imageLabel(image: ImageSummary): string {
  const reference = imageReference(image);
  const openbox = /^openbox:([^/]+)\/(.+)$/u.exec(reference);
  const base = openbox
    ? `${titleCase(openbox[1])} · ${titleCase(openbox[2])}`
    : titleCase(reference);
  if (image.compatibility === "virtual-machine") return `${base} (VM)`;
  if (image.compatibility === "container") return `${base} (container)`;
  return base;
}

function pickImage(images: ImageSummary[], preferred: string): string {
  const match = images.find((image) => {
    const reference = imageReference(image);
    return reference === preferred || image.alias === preferred || image.source.includes(preferred);
  });
  if (match) return imageReference(match);
  return images[0] ? imageReference(images[0]) : preferred;
}

export function CreateInstancePage({ api, onBack, onCreated }: CreateInstancePageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [name, setName] = useState("");
  const [kind, setKind] = useState<InstanceKind>("vps");
  const [image, setImage] = useState("");
  const [isolation, setIsolation] = useState<IsolationRequest>("container");
  const [vcpus, setVcpus] = useState(2);
  const [memoryGib, setMemoryGib] = useState(8);
  const [diskGib, setDiskGib] = useState(20);
  const [packages, setPackages] = useState<string[]>([]);
  const [keyChoice, setKeyChoice] = useState("");
  const [pastedKey, setPastedKey] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    void Promise.all([api.listImages(), api.listSSHKeys(), api.listSoftwareCatalog(), api.getCapabilities()])
      .then(([images, keys, catalog, capabilities]) => {
        if (!active) return;
        setData({ status: "ready", images, keys, catalog, capabilities });
        const defaults = defaultsForKind("vps", capabilities);
        setIsolation(defaults.isolation);
        setImage(pickImage(images, defaults.preferredImage));
        setKeyChoice(keys[0]?.id ?? PASTE_KEY);
      })
      .catch((reason: unknown) => {
        if (active) {
          setData({
            status: "error",
            message: reason instanceof Error ? reason.message : "Create form unavailable",
          });
        }
      });
    return () => { active = false; };
  }, [api]);

  const selectedKey = useMemo(() => {
    if (data.status !== "ready" || keyChoice === PASTE_KEY) return null;
    return data.keys.find((key) => key.id === keyChoice) ?? null;
  }, [data, keyChoice]);

  function applyKind(next: InstanceKind) {
    setKind(next);
    const defaults = defaultsForKind(next, data.status === "ready" ? data.capabilities : undefined);
    setIsolation(defaults.isolation);
    setVcpus(defaults.vcpus);
    setMemoryGib(defaults.memoryGib);
    setDiskGib(defaults.diskGib);
    if (data.status === "ready") {
      setImage(pickImage(data.images, defaults.preferredImage));
    } else {
      setImage(defaults.preferredImage);
    }
  }

  function togglePackage(packageId: string) {
    setPackages((current) => (
      current.includes(packageId)
        ? current.filter((id) => id !== packageId)
        : [...current, packageId]
    ));
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const ownerPublicKey = (selectedKey?.publicKey ?? pastedKey).trim();
    if (!name.trim()) {
      setError("Name is required");
      return;
    }
    if (!image.trim()) {
      setError("Image is required");
      return;
    }
    if (!ownerPublicKey) {
      setError("An SSH public key is required");
      return;
    }
    if (vcpus < 1 || memoryGib < 1 || diskGib < 1) {
      setError("Resources must be at least 1");
      return;
    }
    setPending(true);
    try {
      const result = await api.createInstance({
        name: name.trim(),
        kind,
        image: image.trim(),
        requestedIsolation: isolation,
        vcpus,
        memoryBytes: Math.round(memoryGib * GIB),
        diskBytes: Math.round(diskGib * GIB),
        ownerPublicKey,
        packages,
      });
      let connection: ConnectionInfo = { ssh: null };
      try {
        connection = await api.getConnection();
      } catch {
        connection = { ssh: null };
      }
      setData({ status: "created", result, connection });
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : "Could not create instance");
    } finally {
      setPending(false);
    }
  }

  if (data.status === "created") {
    const instanceName = data.result.instance?.name || name.trim();
    return (
      <div className="console-layout">
        <a className="skip-link" href="#create-instance-main">Skip to create success</a>
        <header className="console-header">
          <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
          <nav aria-label="Primary navigation">
            <button className="nav-button" type="button" onClick={onBack}>Instances</button>
          </nav>
        </header>
        <div className="console-workspace">
          <main id="create-instance-main" tabIndex={-1}>
            <div className="page-heading">
              <div>
                <h1>Instance created</h1>
                <p className="data-message" role="status">{instanceName} is ready to connect.</p>
              </div>
            </div>
            <SSHConnect instanceName={instanceName} connection={data.connection} />
            <div className="create-actions">
              <button className="primary-action" type="button" onClick={() => onCreated(data.result)}>
                Open instance
              </button>
            </div>
          </main>
        </div>
        <footer><span>openbox</span><span>v0.01</span></footer>
      </div>
    );
  }

  return (
    <div className="console-layout">
      <a className="skip-link" href="#create-instance-main">Skip to create form</a>
      <header className="console-header">
        <a className="wordmark" href="/" aria-label="OpenBox home"><span>OB</span> OpenBox</a>
        <nav aria-label="Primary navigation">
          <button className="nav-button" type="button" onClick={onBack}>Instances</button>
        </nav>
      </header>

      <div className="console-workspace">
        <main id="create-instance-main" tabIndex={-1}>
          <div className="page-heading">
            <div>
              <button className="link-button instance-back" type="button" onClick={onBack}>
                ← Instances
              </button>
              <h1>New instance</h1>
            </div>
          </div>

          {data.status === "loading" ? <p className="data-message" role="status">Loading…</p> : null}
          {data.status === "error" ? <p className="data-message is-error" role="alert">{data.message}</p> : null}

          {data.status === "ready" ? (
            <form className="create-instance-form" onSubmit={(event) => { void submit(event); }}>
              <label>
                <span>Name</span>
                <input
                  name="name"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  autoComplete="off"
                  required
                  autoFocus
                />
              </label>

              <fieldset>
                <legend>Kind</legend>
                <div className="create-choice-row" role="group" aria-label="Kind">
                  <button
                    type="button"
                    className={kind === "vps" ? "choice-button is-selected" : "choice-button"}
                    aria-pressed={kind === "vps"}
                    onClick={() => applyKind("vps")}
                  >
                    VPS
                  </button>
                  <button
                    type="button"
                    className={kind === "sandbox" ? "choice-button is-selected" : "choice-button"}
                    aria-pressed={kind === "sandbox"}
                    onClick={() => applyKind("sandbox")}
                  >
                    Sandbox
                  </button>
                </div>
              </fieldset>

              <label>
                <span>Image</span>
                {data.images.length > 0 ? (
                  <select
                    name="image"
                    value={image}
                    onChange={(event) => setImage(event.target.value)}
                    required
                  >
                    {data.images.map((item) => {
                      const reference = imageReference(item);
                      return (
                        <option key={item.id || reference} value={reference}>
                          {imageLabel(item)}
                        </option>
                      );
                    })}
                  </select>
                ) : (
                  <input
                    name="image"
                    value={image}
                    onChange={(event) => setImage(event.target.value)}
                    required
                  />
                )}
              </label>

              <label>
                <span>Isolation</span>
                <select
                  name="isolation"
                  value={isolation}
                  onChange={(event) => setIsolation(event.target.value as IsolationRequest)}
                >
                  <option value="strong">strong (VM)</option>
                  <option value="container">container</option>
                </select>
              </label>

              <div className="create-resource-grid">
                <label>
                  <span>vCPU</span>
                  <input
                    name="vcpus"
                    type="number"
                    min={1}
                    step={1}
                    value={vcpus}
                    onChange={(event) => setVcpus(Number(event.target.value))}
                    required
                  />
                </label>
                <label>
                  <span>Memory (GiB)</span>
                  <input
                    name="memory"
                    type="number"
                    min={1}
                    step={1}
                    value={memoryGib}
                    onChange={(event) => setMemoryGib(Number(event.target.value))}
                    required
                  />
                </label>
                <label>
                  <span>Disk (GiB)</span>
                  <input
                    name="disk"
                    type="number"
                    min={1}
                    step={1}
                    value={diskGib}
                    onChange={(event) => setDiskGib(Number(event.target.value))}
                    required
                  />
                </label>
              </div>

              {data.catalog.length > 0 ? (
                <fieldset>
                  <legend>Packages</legend>
                  <div className="create-package-list">
                    {data.catalog.map((pkg) => (
                      <label key={pkg.id} className="create-package-item">
                        <input
                          type="checkbox"
                          checked={packages.includes(pkg.id)}
                          onChange={() => togglePackage(pkg.id)}
                        />
                        <span>
                          <strong>{pkg.name}</strong>
                          {pkg.description ? <small>{pkg.description}</small> : null}
                        </span>
                      </label>
                    ))}
                  </div>
                </fieldset>
              ) : null}

              <fieldset>
                <legend>SSH public key</legend>
                {data.keys.length > 0 ? (
                  <label>
                    <span>Registered key</span>
                    <select
                      name="ssh-key"
                      value={keyChoice}
                      onChange={(event) => setKeyChoice(event.target.value)}
                    >
                      {data.keys.map((key) => (
                        <option key={key.id} value={key.id}>
                          {key.label || key.fingerprint} — {key.fingerprint}
                        </option>
                      ))}
                      <option value={PASTE_KEY}>Paste a different key…</option>
                    </select>
                  </label>
                ) : null}
                {keyChoice === PASTE_KEY || data.keys.length === 0 ? (
                  <label>
                    <span>{data.keys.length > 0 ? "Public key" : "Paste public key"}</span>
                    <textarea
                      name="owner-public-key"
                      rows={4}
                      value={pastedKey}
                      onChange={(event) => setPastedKey(event.target.value)}
                      placeholder="ssh-ed25519 AAAA… comment"
                      required={keyChoice === PASTE_KEY || data.keys.length === 0}
                    />
                  </label>
                ) : null}
              </fieldset>

              <p className="form-error" role={error ? "alert" : undefined} aria-live="assertive">
                {error}
              </p>

              <div className="create-actions">
                <button className="primary-action" type="submit" disabled={pending}>
                  {pending ? "Creating…" : "Create instance"}
                </button>
                <button className="btn" type="button" onClick={onBack} disabled={pending}>
                  Cancel
                </button>
              </div>
            </form>
          ) : null}
        </main>
      </div>
      <footer><span>openbox</span><span>v0.01</span></footer>
    </div>
  );
}
