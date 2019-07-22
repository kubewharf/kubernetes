#!/bin/bash

set -e

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root_dir=$script_dir/..

cd $root_dir

# NOTE: Shell behaviors may be cached when `GOCACHE` is enabled, so it's
# necessory to disable it explicitly.
GOCACHE=off go test -v .
