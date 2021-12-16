#!/bin/bash

if [[ -n "$(git status --porcelain .)" ]]; then
    echo "Uncommitted files. Run 'make test' and commit changes."
    echo "$(git status --porcelain .)"
    exit 1
fi
