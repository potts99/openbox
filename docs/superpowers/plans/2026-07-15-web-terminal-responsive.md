# Web Terminal Responsive Immersion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Immersive, full-viewport browser terminal with auto-hiding chrome on desktop/tablet/mobile, always opening the persistent tmux session `main`.

**Architecture:** Keep `InstanceTerminal` as the thin UI owner. Always pass `sessionName: "main"` into `TerminalSession`. Replace the persistent-session form with overlay chrome visibility state (forced on connecting/error/disconnect; auto-hide ~1.5s after connected; reveal via top affordance, Escape, or desktop hover). CSS makes the PTY edge-to-edge with `100dvh` and safe-area insets. No protocol or Fullscreen API changes.

**Tech Stack:** React + TypeScript, Vitest + Testing Library, xterm.js + FitAddon, existing console CSS (`web/src/styles.css`).

**Spec:** `docs/superpowers/specs/2026-07-15-web-terminal-responsive-design.md`

## Global Constraints

- Always open with `session_name: "main"`; no ephemeral browser terminals.
- Remove Persistent checkbox and session-name input; no multi-session UI.
- No on-screen accessory key bar; no `requestFullscreen`.
- No protocol/auth/CSRF/audit changes beyond always sending `sessionName: "main"`.
- Preserve skip-link, connection `role="status"` / `aria-live`, Terminate + Reconnect.
- Match existing dark mono terminal aesthetic (IBM Plex Mono / current tokens); no purple/glow redesign.
- YAGNI: soft-keyboard geometry beyond `100dvh` / safe-area / fit-on-resize is out of scope.

---

## File structure

| Path | Responsibility |
|------|----------------|
| `web/src/pages/InstanceTerminal.tsx` | Always-`main` open; chrome visibility state; reveal/hide handlers; simplified toolbar markup |
| `web/src/pages/InstanceTerminal.test.tsx` | Always-`main`, no session form, chrome visibility behaviors |
| `web/src/terminal/TerminalSurface.tsx` | Font/theme/padding polish only |
| `web/src/styles.css` | Immersive page, overlay chrome, reveal strip, breakpoints, safe-area, body scroll lock |
| `docs/development/browser-terminal.md` | Note that the dashboard always uses session `main` |

No new packages or routes. `TerminalSession` / protocol stay as-is (page supplies the name).

---

### Task 1: Always-`main` + remove session UI

**Files:**
- Modify: `web/src/pages/InstanceTerminal.tsx`
- Modify: `web/src/pages/InstanceTerminal.test.tsx`
- Modify: `docs/development/browser-terminal.md` (short note under named sessions)

**Interfaces:**
- Consumes: `TerminalSession` options `sessionName?: string` (existing)
- Produces: every attach/open uses `sessionName: "main"`; no persistent UI state

- [ ] **Step 1: Replace the persistent-session test with always-`main`**

In `web/src/pages/InstanceTerminal.test.tsx`, replace the test `"exposes persistent session controls and opens with session_name when enabled"` with:

```tsx
it("always opens the persistent main session and has no session name controls", async () => {
  renderTerminal();

  expect(screen.queryByRole("checkbox", { name: /persistent session/i })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "Session name" })).toBeNull();

  const socket = FakeWebSocket.instances.at(-1);
  socket?.open();
  expect(JSON.parse(socket?.sent[0] ?? "{}")).toMatchObject({
    type: "open",
    session_name: "main",
  });
  socket?.message(encodeFrame({
    type: "open",
    instanceId: "box-1",
    cols: 80,
    rows: 24,
    sessionName: "main",
  }));
  expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/connected/i);
});
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx -t "always opens the persistent main"`

Expected: FAIL — open frame lacks `session_name: "main"` and/or checkbox still present.

- [ ] **Step 3: Simplify `InstanceTerminal` to always use `main`**

In `web/src/pages/InstanceTerminal.tsx`:

1. Remove `persistent` / `sessionName` state and their refs (`persistentRef`, `sessionNameRef`, `openedSessionNameRef` comparison against user input).
2. Add a module constant:

```ts
const PERSISTENT_SESSION_NAME = "main";
```

3. In `attachSession`, always pass:

```ts
sessionName: PERSISTENT_SESSION_NAME,
```

and set `openedSessionNameRef.current = PERSISTENT_SESSION_NAME` (or drop the reopen-name comparison and always `reconnect()` when a session already exists — either is fine as long as reopen still uses `main`).

4. Remove the persistent fieldset from the toolbar JSX (checkbox + session name input). Keep Back, heading, connection status, Reconnect, Terminate.

5. Delete `onPersistentChange` / `onSessionNameChange`.

- [ ] **Step 4: Run the full InstanceTerminal suite**

Run: `cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx`

Expected: PASS (all tests green).

- [ ] **Step 5: Doc note**

In `docs/development/browser-terminal.md`, under named tmux sessions, add one sentence:

> The dashboard terminal always opens `session_name: main` (no ephemeral shell UI).

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/InstanceTerminal.tsx web/src/pages/InstanceTerminal.test.tsx docs/development/browser-terminal.md
git commit -m "$(cat <<'EOF'
feat: always open browser terminal as tmux main

Remove opt-in persistent session controls so every dashboard terminal attaches the same named session.
EOF
)"
```

---

### Task 2: Immersive layout CSS (edge-to-edge PTY)

**Files:**
- Modify: `web/src/styles.css` (terminal section ~604–653)
- Modify: `web/src/pages/InstanceTerminal.tsx` (class names + move hint into toolbar)

**Interfaces:**
- Consumes: existing `.terminal-*` classes
- Produces: `.terminal-page` fills viewport; `.terminal-main` is a single full-bleed surface; hint lives in chrome

- [ ] **Step 1: Relocate the clipboard hint into the toolbar**

In `InstanceTerminal.tsx`, move the hint paragraph from under the surface into the toolbar (or make it `aria`-friendly text next to status). Remove the bottom hint from `<main>` so main is only the surface:

```tsx
<main id="terminal-main" className="terminal-main" aria-labelledby="terminal-heading">
  <Surface onData={onData} onResize={onResize} onReady={onReady} />
</main>
```

Keep a short hint in the header, e.g.:

```tsx
<p className="terminal-hint">Copy/paste uses the system clipboard.</p>
```

inside `.terminal-actions` or under the title block (visually secondary; can be `sr-only` on narrow screens via CSS in Task 4).

- [ ] **Step 2: Update terminal CSS for full-viewport surface**

Replace the terminal block in `web/src/styles.css` with immersive layout (keep dark tokens):

```css
/* Terminal */
.terminal-page {
  position: relative;
  display: grid;
  grid-template-rows: 1fr;
  height: 100dvh;
  min-height: 100vh;
  overflow: hidden;
  background: #111110;
  color: #e8e6df;
  padding: env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left);
}
.terminal-toolbar {
  position: absolute;
  z-index: 5;
  left: 0;
  right: 0;
  top: 0;
  display: flex;
  flex-wrap: wrap;
  gap: .5rem 1rem;
  align-items: center;
  padding: .55rem .75rem;
  border-bottom: 1px solid #2a2a27;
  background: rgba(22, 22, 20, .96);
}
.terminal-toolbar h1 {
  margin: 0;
  font-size: .9375rem;
  font-weight: 500;
  font-family: "IBM Plex Mono", ui-monospace, monospace;
}
.terminal-toolbar button { border-color: #3a3a36; background: transparent; color: inherit; }
.terminal-toolbar button:hover:not(:disabled) { background: #222220; color: inherit; border-color: #5a5a54; }
.terminal-actions { display: flex; align-items: center; gap: .5rem; flex-wrap: wrap; margin-left: auto; }
.terminal-connection {
  margin: 0;
  color: #9a978c;
  font-size: .7rem;
  font-family: "IBM Plex Mono", ui-monospace, monospace;
}
.terminal-main {
  min-height: 0;
  height: 100%;
  padding: 0;
}
.terminal-surface {
  min-height: 0;
  height: 100%;
  border: 0;
  background: #0c0c0b;
}
.terminal-surface .xterm { height: 100%; padding: .35rem .45rem; }
.terminal-hint {
  margin: 0;
  color: #6b6b66;
  font-size: .7rem;
  font-family: "IBM Plex Mono", ui-monospace, monospace;
}
```

(Chrome hide/reveal classes land in Task 3; this task only makes the PTY fill the viewport with overlay-positioned toolbar.)

- [ ] **Step 3: Re-run InstanceTerminal tests**

Run: `cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx`

Expected: PASS. If axe or queries break because hint moved, update selectors only as needed (heading/status/buttons unchanged).

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/InstanceTerminal.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
feat: make terminal PTY fill the viewport

Overlay the toolbar and drop the bottom hint so the surface can use the full screen.
EOF
)"
```

---

### Task 3: Chrome show/hide + reveal affordance

**Files:**
- Modify: `web/src/pages/InstanceTerminal.tsx`
- Modify: `web/src/pages/InstanceTerminal.test.tsx`
- Modify: `web/src/styles.css`

**Interfaces:**
- Produces: `chromeVisible` driven by connection state + user reveal; CSS modifiers `.terminal-toolbar--hidden` / `.terminal-reveal`

- [ ] **Step 1: Write failing chrome-visibility tests**

Add to `InstanceTerminal.test.tsx` (use fake timers):

```tsx
it("auto-hides chrome after connect and reveals it on Escape or disconnect", async () => {
  vi.useFakeTimers();
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  renderTerminal();

  const toolbar = document.querySelector(".terminal-toolbar");
  expect(toolbar).not.toHaveClass("terminal-toolbar--hidden");

  openConnectedSocket();
  await screen.findByRole("status", { name: "Terminal connection state" });
  await vi.advanceTimersByTimeAsync(1600);
  expect(toolbar).toHaveClass("terminal-toolbar--hidden");

  await user.keyboard("{Escape}");
  expect(toolbar).not.toHaveClass("terminal-toolbar--hidden");

  await user.keyboard("{Escape}");
  expect(toolbar).toHaveClass("terminal-toolbar--hidden");

  FakeWebSocket.instances[0]?.close();
  expect(await screen.findByRole("status", { name: "Terminal connection state" })).toHaveTextContent(/disconnected/i);
  expect(toolbar).not.toHaveClass("terminal-toolbar--hidden");
  expect(screen.getByRole("button", { name: "Reconnect" })).toBeInTheDocument();

  vi.useRealTimers();
});

it("reveals chrome when the top affordance is activated", async () => {
  vi.useFakeTimers();
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
  renderTerminal();
  openConnectedSocket();
  await screen.findByRole("status", { name: "Terminal connection state" });
  await vi.advanceTimersByTimeAsync(1600);

  await user.click(screen.getByRole("button", { name: "Show terminal controls" }));
  expect(document.querySelector(".terminal-toolbar")).not.toHaveClass("terminal-toolbar--hidden");

  vi.useRealTimers();
});
```

Wrap each test with `afterEach` / try-finally to restore real timers if the suite does not already.

- [ ] **Step 2: Run new tests — expect FAIL**

Run: `cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx -t "chrome"`

Expected: FAIL — no `--hidden` class / no affordance button.

- [ ] **Step 3: Implement chrome visibility in `InstanceTerminal`**

```tsx
const CHROME_HIDE_MS = 1500;

const [chromeVisible, setChromeVisible] = useState(true);
const chromeFocusRef = useRef(false);
const surfaceHostRef = useRef<HTMLElement | null>(null);

// Force visible on non-connected; schedule hide on connected
useEffect(() => {
  if (connection.status !== "connected") {
    setChromeVisible(true);
    return;
  }
  if (chromeFocusRef.current) return;
  const timer = window.setTimeout(() => {
    if (!chromeFocusRef.current) setChromeVisible(false);
  }, CHROME_HIDE_MS);
  return () => window.clearTimeout(timer);
}, [connection.status]);

const revealChrome = useCallback(() => setChromeVisible(true), []);
const hideChromeIfConnected = useCallback(() => {
  if (connection.status === "connected" && !chromeFocusRef.current) {
    setChromeVisible(false);
  }
}, [connection.status]);

useEffect(() => {
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key !== "Escape") return;
    if (!chromeVisible) {
      event.preventDefault();
      setChromeVisible(true);
      return;
    }
    if (connection.status === "connected") {
      event.preventDefault();
      setChromeVisible(false);
      surfaceHostRef.current?.querySelector<HTMLElement>(".terminal-surface")?.focus();
    }
  };
  window.addEventListener("keydown", onKeyDown);
  return () => window.removeEventListener("keydown", onKeyDown);
}, [chromeVisible, connection.status]);
```

Markup:

```tsx
<button
  type="button"
  className="terminal-reveal"
  aria-label="Show terminal controls"
  hidden={chromeVisible}
  onClick={revealChrome}
  onMouseEnter={revealChrome}
/>
<header
  className={`terminal-toolbar${chromeVisible ? "" : " terminal-toolbar--hidden"}`}
  onFocusCapture={() => { chromeFocusRef.current = true; setChromeVisible(true); }}
  onBlurCapture={(event) => {
    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
      chromeFocusRef.current = false;
      hideChromeIfConnected();
    }
  }}
  onMouseEnter={revealChrome}
>
  {/* existing controls without session fieldset */}
</header>
<main ref={surfaceHostRef} ...>
```

When `chromeVisible` becomes false after connect, focus the `.terminal-surface` if focus was not inside chrome.

- [ ] **Step 4: CSS for hidden chrome + reveal strip**

```css
.terminal-toolbar--hidden {
  transform: translateY(-100%);
  pointer-events: none;
  opacity: 0;
  transition: transform .18s ease, opacity .18s ease;
}
.terminal-toolbar {
  transition: transform .18s ease, opacity .18s ease;
}
.terminal-reveal {
  position: absolute;
  z-index: 6;
  top: 0;
  left: 0;
  right: 0;
  height: 24px;
  margin: 0;
  padding: 0;
  border: 0;
  background: transparent;
  cursor: pointer;
}
.terminal-reveal[hidden] { display: none; }
@media (prefers-reduced-motion: reduce) {
  .terminal-toolbar,
  .terminal-toolbar--hidden { transition: none; }
}
```

- [ ] **Step 5: Run full InstanceTerminal suite**

Run: `cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx`

Expected: PASS. Fix timer/userEvent quirks if needed (`vi.useFakeTimers({ shouldAdvanceTime: true })` only if required).

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/InstanceTerminal.tsx web/src/pages/InstanceTerminal.test.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
feat: auto-hide terminal chrome after connect

Reveal controls via the top affordance, Escape, or disconnect so the PTY stays immersive.
EOF
)"
```

---

### Task 4: Breakpoint polish + body scroll lock

**Files:**
- Modify: `web/src/styles.css`
- Modify: `web/src/pages/InstanceTerminal.tsx` (body class on mount)

- [ ] **Step 1: Lock body scroll while the terminal page is mounted**

In `InstanceTerminal.tsx`:

```tsx
useEffect(() => {
  const { classList } = document.body;
  classList.add("terminal-open");
  return () => classList.remove("terminal-open");
}, []);
```

CSS:

```css
body.terminal-open { overflow: hidden; }
```

- [ ] **Step 2: Add tablet/mobile toolbar rules**

Append after the terminal block:

```css
@media (max-width: 900px) {
  .terminal-toolbar { gap: .45rem .75rem; padding: .5rem .65rem; }
  .terminal-toolbar button { padding: .5rem .7rem; min-height: 2.25rem; }
  .terminal-reveal { height: 28px; }
}

@media (max-width: 720px) {
  .terminal-toolbar {
    align-items: flex-start;
  }
  .terminal-actions {
    width: 100%;
    margin-left: 0;
    justify-content: flex-start;
  }
  .terminal-hint { display: none; }
  .terminal-toolbar h1 {
    font-size: .875rem;
    max-width: 12rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}
```

- [ ] **Step 3: Re-run tests**

Run: `cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx`

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/InstanceTerminal.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
fix: tighten terminal chrome on tablet and mobile

Lock page scroll and stack actions so the immersive toolbar stays usable on small screens.
EOF
)"
```

---

### Task 5: Surface polish + verification

**Files:**
- Modify: `web/src/terminal/TerminalSurface.tsx`
- Optional touch: `web/src/styles.css` if contrast tweaks needed

- [ ] **Step 1: Align xterm font with console mono**

In `XtermTerminalSurface` options:

```ts
const term = new Terminal({
  cursorBlink: true,
  fontFamily: '"IBM Plex Mono", ui-monospace, "SFMono-Regular", Menlo, monospace',
  fontSize: 14,
  theme: {
    background: "#0c0c0b",
    foreground: "#e8e6df",
    cursor: "#e64e20",
    selectionBackground: "#3a3a36",
  },
});
```

Keep FitAddon + ResizeObserver behavior unchanged.

- [ ] **Step 2: Run web terminal tests**

Run:

```bash
cd web && pnpm exec vitest run src/pages/InstanceTerminal.test.tsx src/terminal/session.test.ts src/terminal/protocol.test.ts
```

Expected: PASS.

- [ ] **Step 3: Manual smoke checklist** (engineer or local dashboard)

1. Desktop: open terminal → chrome hides ~1.5s after Connected → hover/click top strip or Escape reveals → Escape hides again.
2. Narrow viewport (≤720): same auto-hide; actions wrap; no bottom hint; PTY fills height.
3. Disconnect: chrome forced visible + Reconnect works; reopen still `main`.
4. Back to instance leaves session (no terminate); Terminate still sends TERM.
5. Resize/rotate: cols/rows update (FitAddon).

- [ ] **Step 4: Commit**

```bash
git add web/src/terminal/TerminalSurface.tsx web/src/styles.css
git commit -m "$(cat <<'EOF'
style: align xterm surface with immersive terminal chrome

Match IBM Plex Mono and background tokens so the PTY and overlay read as one surface.
EOF
)"
```

---

## Spec coverage check

| Spec requirement | Task |
|------------------|------|
| Always `session_name: main` | Task 1 |
| Remove persistent UI | Task 1 |
| Overlay immersive layout / `100dvh` / safe-area | Task 2 |
| Hint not shrinking PTY | Task 2 |
| Auto-hide after connected | Task 3 |
| Reveal: affordance / Escape / disconnect force | Task 3 |
| Focus return to surface | Task 3 |
| Desktop/tablet/mobile breakpoints | Task 4 |
| Body scroll lock on mobile | Task 4 |
| Visual mono polish | Task 5 |
| Unit tests updated | Tasks 1, 3, 5 |
| No accessory bar / Fullscreen / protocol change | Global constraints |

## Placeholder / consistency review

- Constant name: `PERSISTENT_SESSION_NAME = "main"` (Task 1) — used everywhere.
- Hide delay: `CHROME_HIDE_MS = 1500` — tests advance `1600`.
- CSS class: `terminal-toolbar--hidden` + `terminal-reveal` — shared by Tasks 3–4.
- No TBD/TODO left in steps.
