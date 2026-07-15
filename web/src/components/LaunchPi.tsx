// SPDX-License-Identifier: AGPL-3.0-only

interface LaunchPiProps {
  disabled?: boolean;
  onLaunch(): void;
}

export function LaunchPi({ disabled = false, onLaunch }: LaunchPiProps) {
  return (
    <button
      className="primary-action"
      type="button"
      disabled={disabled}
      onClick={onLaunch}
    >
      Launch Pi
    </button>
  );
}
