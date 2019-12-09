#!/usr/bin/env bash

export GO111MODULE=off
bash ./hack/update-tracing-packages.sh

make hyperkube && \
  mkdir -p output && \
  mv _output/local/go/bin/hyperkube output/
