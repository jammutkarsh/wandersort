#!/usr/bin/env bash
# Copyright (c) 2026 Utkarsh Chourasia
#
# This file is part of WanderSort.
#
# SPDX-License-Identifier: AGPL-3.0-or-later

# Checks every .go file starts with the expected copyright/license header.
# Usage: scripts/check-license.sh (exits non-zero and lists offenders on failure)
set -euo pipefail
cd "$(dirname "$0")/.."

expected='// Copyright (c) 2026 Utkarsh Chourasia
//
// This file is part of WanderSort.
//
// SPDX-License-Identifier: AGPL-3.0-or-later'

missing=()
while IFS= read -r -d '' file; do
	if [ "$(head -n 5 "$file")" != "$expected" ]; then
		missing+=("$file")
	fi
done < <(find . -name '*.go' -not -path './bin/*' -not -path './.git/*' -print0)

if [ ${#missing[@]} -gt 0 ]; then
	echo "Missing/incorrect copyright header:"
	printf '  %s\n' "${missing[@]}"
	exit 1
fi

echo "OK: every .go file has the expected copyright header."
