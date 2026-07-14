#!/usr/bin/env bash
# SPDX-License-Identifier: AGPL-3.0-only

set -euo pipefail

status=0
while IFS= read -r file; do
  if ! head -n 3 "$file" | grep -q "SPDX-License-Identifier: AGPL-3.0-only"; then
    echo "missing AGPL SPDX header: $file" >&2
    status=1
  fi
done < <(find cmd internal web/src web/tests -type f \( -name '*.go' -o -name '*.ts' -o -name '*.tsx' \) -print | sort)

exit "$status"
