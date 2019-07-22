#!/bin/bash

set -e

# a script for updating godep dependencies for the vendored directory /cmd/
# without pulling in kube-tracing itself as a dependency.
#
# update depedency
# 1. edit glide.yaml with version, git sha
# 2. run ./scripts/update-dependencies.sh
# 3. it automatically detects new git sha, and vendors updates to cmd/vendor directory
#
# add depedency
# 1. run ./scripts/update-dependencies.sh github.com/user/project#^1.0.0
#        or
#        ./scripts/update-dependencies.sh github.com/user/project#9b772b54b3bf0be1eec083c9669766a56332559a
# 2. make sure glide.yaml and glide.lock are updated

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root_dir=$script_dir/..

cd $root_dir

rm -rf vendor dependencies/vendor
mkdir -p dependencies

gopath=/tmp/_gopath
rm -rf $gopath
mkdir -p $gopath 
export GOPATH=$gopath
export PATH=$PATH:$GOPATH/bin

setup_proxy() {
    export http_proxy=http://10.3.21.198:3128
    export https_proxy=http://10.3.21.198:3128
}

unset_proxy() {
    unset http_proxy
    unset https_proxy
}

install_glide() {
    glide_root="$gopath/src/github.com/Masterminds/glide"
    glide_sha=21ff6d397ccca910873d8eaabab6a941c364cc70
    # setup_proxy
    go get -d -u github.com/Masterminds/glide
    # unset_proxy
    pushd "${glide_root}"
    git reset --hard ${glide_sha}
    go install
    popd
}

install_glide_vc() {
    glide_vc_root="$gopath/src/github.com/sgotti/glide-vc"
    glide_vc_sha=d96375d23c85287e80296cdf48f9d21c227fa40a
    # setup_proxy
    go get -d -u github.com/sgotti/glide-vc
    # unset_proxy
    pushd "${glide_vc_root}"
    git reset --hard ${glide_vc_sha}
    go install
    popd
}

update_dependencies() {
    if [ -n "$1" ]; then
    	matches=`grep "name: $1" glide.lock`
    	if [ ! -z "$matches" ]; then
    		glide update --strip-vendor $1
    	else
    		glide get --strip-vendor $1
    	fi
    else
    	glide update --strip-vendor
    fi
    
    glide vc --only-code --no-tests
}

cleanup() {
    mv vendor dependencies/
    rm -rf $gopath
}

install_glide
install_glide_vc
update_dependencies $1
cleanup
