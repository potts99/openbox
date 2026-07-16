// SPDX-License-Identifier: AGPL-3.0-only

import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import type {
  CreateInstanceResult,
  ImageSummary,
  InstanceKind,
  IsolationRequest,
  OpenBoxApi,
  SoftwarePackage,
  SSHKeySummary,
} from "../api/client";

interface CreateInstancePageProps {
  api: OpenBoxApi;
  onBack(): void;
  onCreated(result: CreateInstanceResult): void;
}

type PageData =
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; images: ImageSummary[]; keys: SSHKeySummary[]; catalog: SoftwarePackage[] };

interface KindDefaults {
  isolation: IsolationRequest;
  vcpus: number;
  memoryGib: number;
  diskGib: number;
  preferredImage: string;
}

const PASTE_KEY = "__paste__";
const GIB = 1024 ** 3;

function defaultsForKind(kind: InstanceKind): KindDefaults {
  if (kind === "sandbox") {
    return {
      isolation: "standard",
      vcpus: 2,
      memoryGib: 2,
      diskGib: 10,
      preferredImage: "openbox:sandbox/ubuntu/24.04",
    };
  }
  return {
    isolation: "best_available",
    vcpus: 2,
    memoryGib: 8,
    diskGib: 20,
    preferredImage: "ubuntu",
  };
}

function pickImage(images: ImageSummary[], preferred: string): string {
  if (images.some((image) => image.alias === preferred)) return preferred;
  return images[0]?.alias ?? preferred;
}

export function CreateInstancePage({ api, onBack, onCreated }: CreateInstancePageProps) {
  const [data, setData] = useState<PageData>({ status: "loading" });
  const [name, setName] = useState("");
  const [kind, setKind] = useState<InstanceKind>("vps");
  const [image, setImage] = useState("");
  const [isolation, setIsolation] = useState<IsolationRequest>("best_available");
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
    void Promise.all([api.listImages(), api.listSSHKeys(), api.listSoftwareCatalog()])
      .then(([images, keys, catalog]) => {
        if (!active) return;
        setData({ status: "ready", images, keys, catalog });
        const defaults = defaultsForKind("vps");
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
    const defaults = defaultsForKind(next);
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
      onCreated(result);
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : "Could not create instance");
    } finally {
      setPending(false);
    }
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
                <div className="create-choice-row">
                  <label className="choice-pill">
                    <input
                      type="radio"
                      name="kind"
                      checked={kind === "vps"}
                      onChange={() => applyKind("vps")}
                    />
                    <span>VPS</span>
                  </label>
                  <label className="choice-pill">
                    <input
                      type="radio"
                      name="kind"
                      checked={kind === "sandbox"}
                      onChange={() => applyKind("sandbox")}
                    />
                    <span>Sandbox</span>
                  </label>
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
                    {data.images.map((item) => (
                      <option key={item.id || item.alias} value={item.alias}>
                        {item.alias}{item.architecture ? ` (${item.architecture})` : ""}
                      </option>
                    ))}
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
                  <option value="best_available">best_available</option>
                  <option value="standard">standard</option>
                  <option value="strong">strong</option>
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
      <footer><span>openbox</span><span>v1</span></footer>
    </div>
  );
}
