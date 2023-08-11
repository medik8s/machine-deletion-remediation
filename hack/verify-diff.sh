#!/bin/bash

status=0
# Checks that there are no uncommitted changes.
if [[ -n "$(git status --porcelain .)" ]]; then
    echo "[!] Uncommitted files. Commit changes and run 'make test'."
    # Ignore untracked files to avoid messing up with the CI.
    echo "$(git status --porcelain .)"
    let status=1
fi

# Checks that changes under config directory are reflected in the bundle directory.
# This is to ensure that the bundle is up-to-date with the latest changes.
# If this fails, run 'make bundle' to update the bundle.
if [[ -n "$(git status --porcelain config/)" ]] && [[ -z "$(git status --porcelain bundle/)"  ]]; then
    echo ""
    echo "[!] Config folder has changes, but Bundle does not. Check if Bundle is out-of-date."
    echo "$(git status --porcelain config/)"
    let status=1
fi

exit $status
