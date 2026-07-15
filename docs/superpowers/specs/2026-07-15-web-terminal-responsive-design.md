# Web terminal responsive immersion — design

**Status:** Approved for implementation (Approach 2: structured immersive chrome)  
**Date:** 2026-07-15  
**Surface:** Browser terminal (`InstanceTerminal`, `XtermTerminalSurface`, terminal CSS)

## Goal

Make the instance web terminal usable and visually consistent on desktop, tablet, and mobile: maximize PTY space with immersive auto-hiding chrome, always reconnect to a single persistent tmux session (`main`), and polish layout/typography without adding on-screen key helpers.

## Non-goals

- On-screen accessory keys (Ctrl, Esc, Tab, arrows, paste bar)
- Browser Fullscreen API
- Multiple named sessions or a visible session-name field
- Ephemeral (non-tmux) browser terminals
- Protocol, auth, CSRF, or audit changes beyond always sending `session_name: "main"`
- Mobile soft-keyboard geometry hacks beyond `100dvh` / safe-area / fit-on-resize

## Decisions

| Topic | Choice |
|-------|--------|
| Persistence | Always open with `session_name: "main"` (tmux attach-or-create) |
| Session UI | Remove Persistent checkbox and session-name input |
| Chrome | Overlay toolbar; auto-hide after connected; reveal on demand |
| Fullscreen | SPA immersive only (no `requestFullscreen`) |
| Touch keys | None this pass |
| Clipboard hint | Shorten or move into chrome so it does not shrink the PTY on small screens |

## Behavior

### Session model

- Every `InstanceTerminal` open always passes `sessionName: "main"` into `TerminalSession` / the WebSocket `open` frame.
- Detach, tab close, and **Back to instance** leave the `main` session alive on the guest (existing named-session detach semantics).
- **Reconnect** reattaches when the connection is disconnected/error and the same `main` session still applies.
- **Terminate** keeps existing terminate semantics (ends the live console session).

### Chrome visibility

Chrome contains: Back, instance name, connection status, Reconnect (when applicable), Terminate.

| Connection state | Chrome |
|------------------|--------|
| `connecting` | Forced visible |
| `connected` | Auto-hide after ~1.5s (unless focus is inside a chrome control) |
| `disconnected` / `error` | Forced visible; show Reconnect |
| Terminate in flight | Stay visible until state settles |

**Reveal:** tap/click a thin top-edge hit-target / affordance, or Escape (when chrome is hidden).  
**Hide again:** Escape when chrome is visible and status is `connected`, or after idle once focus returns to the PTY.

### Focus / a11y

- Skip-link and connection `role="status"` / `aria-live` remain.
- When chrome hides, focus returns to the terminal surface.
- When chrome shows, controls are reachable without a focus trap.
- Escape: hidden → show; visible + connected → hide (does not send Escape to the PTY while chrome is visible).

## Layout by breakpoint

Shared: `terminal-page` fills `100dvh` with `env(safe-area-inset-*)`; PTY is edge-to-edge under the overlay; FitAddon + ResizeObserver retained.

| Viewport | Chrome when revealed |
|----------|----------------------|
| Desktop ≥901px | Single compact row; hover/focus on top ~24px strip also reveals |
| Tablet 721–900px | Same immersive model; slightly larger touch targets; may wrap to two rows |
| Mobile ≤720px | Compact overlay: row 1 Back + name + status; row 2 actions; lock body scroll while open |

## Components

| Piece | Change |
|-------|--------|
| `InstanceTerminal` | Always `main`; remove persistent UI; own chrome visibility state + reveal/hide handlers |
| `XtermTerminalSurface` | Theme/font/padding alignment only; still fit-on-resize |
| `styles.css` | Immersive page, overlay toolbar, reveal strip, breakpoint rules, safe-area |
| `TerminalSession` / protocol | No frame-shape changes; page always supplies `sessionName: "main"` |

No new route or package. Chrome markup stays in `InstanceTerminal` with CSS classes for overlay + affordance.

## Visual polish

- Align terminal page with existing console dark mono tokens (IBM Plex Mono / current CSS variables where applicable).
- xterm theme stays dark with the existing accent cursor; avoid purple/glow trends.
- Toolbar buttons match current terminal button styling; denser padding on small screens.
- Connection status remains muted mono text; errors stay readable when chrome is forced visible.

## Testing

- Update `InstanceTerminal.test.tsx`: remove persistent/name UI coverage; assert open uses `main`; assert chrome forced-visible on disconnect/error (and Reconnect available).
- Keep / lightly extend `session` named-open tests as needed; no backend protocol change required for always-`main`.
- Manual check: desktop hover reveal, mobile top-edge tap, Escape toggle, rotate/resize fit, safe-area on notched devices if available.

## Implementation order

1. Always-`main` wiring + remove session UI; fix unit tests.
2. Immersive layout CSS (`100dvh`, safe-area, edge-to-edge surface, hint relocation).
3. Chrome show/hide state + reveal affordance + Escape/focus behavior.
4. Breakpoint polish (tablet/mobile stacking, body scroll lock).
5. Visual pass on toolbar/surface tokens; final test + manual smoke.
