// SPDX-License-Identifier: AGPL-3.0-only

interface LandingPageProps {
  onSignIn(): void;
}

export function LandingPage({ onSignIn }: LandingPageProps) {
  return (
    <div className="landing">
      <a className="skip-link" href="#main-content">Skip to content</a>
      <header className="landing-top">
        <a className="wordmark" href="/">OpenBox</a>
        <button className="nav-button" type="button" onClick={onSignIn}>
          Sign in
        </button>
      </header>
      <main className="landing-hero" id="main-content">
        <p className="landing-brand">OpenBox</p>
        <h1>Self-hosted sandboxes for you and your agents.</h1>
        <p className="landing-lede">
          Persistent environments and disposable Linux sandboxes on infrastructure you control —
          with one API, CLI, and operator console.
        </p>
        <div className="landing-cta">
          <button className="primary-action" type="button" onClick={onSignIn}>
            Open console
          </button>
          <a className="landing-link" href="https://github.com/openbox-dev/openbox">
            View source
          </a>
        </div>
        <ul className="landing-points">
          <li>Agent sandboxes with restricted egress by default</li>
          <li>Disk checkpoints, restore, and clone for fan-out work</li>
          <li>Install on your host — no OpenBox-operated control plane required</li>
        </ul>
      </main>
      <footer className="landing-footer">
        <span>Self-hosted first.</span>
        <span>AGPL-3.0-only.</span>
      </footer>
    </div>
  );
}
