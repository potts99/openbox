// SPDX-License-Identifier: AGPL-3.0-only

/**
 * Copy/paste behavior for the browser terminal.
 *
 * Copy: xterm.js selection + the browser clipboard (Ctrl/Cmd+C when text is
 * selected, or the OS copy shortcut after selecting with the mouse). Selected
 * terminal text is also available via the standard selection clipboard.
 *
 * Paste: Ctrl/Cmd+V and the context-menu paste path deliver clipboard text into
 * the PTY as an input frame (base64 JSON), never logged by the UI layer.
 */

export function encodePasteText(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

export function shouldInterceptCopy(hasSelection: boolean, key: string, metaKey: boolean, ctrlKey: boolean): boolean {
  const modifier = metaKey || ctrlKey;
  return hasSelection && modifier && key.toLowerCase() === "c";
}
