#!/usr/bin/env bash

set -e
set -o pipefail

# This script update dependencies in kube-tracing and link them in
# the root vendor directory. It's easy to merge with upstream in
# this way.

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root_dir=$script_dir/..
tracing_src_dir=$root_dir/third_party/kube-tracing
tracing_link_dir=$root_dir/vendor/code.byted.org/tce/kube-tracing

# The ~/.glide/cache not responding well to git push -f'd repos.
# ref issue: https://github.com/Masterminds/glide/issues/398
clear_glide_cache() 
{
    rm -rf ~/.glide/cache/*
}

fetch_kube_tracing_source_code() {
    rm -rf $tracing_src_dir
    mkdir -p $tracing_src_dir
    git clone git@code.byted.org:tce/kube-tracing.git $tracing_src_dir
    
    pushd $tracing_src_dir
    scripts/update-dependencies.sh
    popd
}

mkdir_if_not_exists() {
    if [ ! -d $1 ]; then
        mkdir $1 
    fi
}

remove_link_if_exists() {
    if [ -L $1 ]; then 
        rm $1 
    fi
}

# Link dependencies of kube-tracing to the root vendor directory. 
link_kube_tracing_dependencies() {
    third_party_vendor_dir=$root_dir/third_party/kube-tracing/dependencies/vendor
    for urlbase in $(ls -d $third_party_vendor_dir/*); do
        url=$(basename $urlbase)
        mkdir_if_not_exists $root_dir/vendor/$url

        for groupbase in $(ls -d $third_party_vendor_dir/$url/*); do
            group=$(basename $groupbase)
            mkdir_if_not_exists $root_dir/vendor/$url/$group

            for projectbase in $(ls -d $third_party_vendor_dir/$url/$group/*); do
                project=$(basename $projectbase)
                remove_link_if_exists $root_dir/vendor/$url/$group/$project

                # Link from kube-tracing dependencies directory to avoid polluting root vendor.
                if [ ! -d $root_dir/vendor/$url/$group/$project ]; then
                    echo "link $root_dir/$projectbase to $root_dir/vendor/$url/$group/$project"
                    ln -sf $projectbase $root_dir/vendor/$url/$group/$project
                fi
            done
        done
    done
}

# Link kube-tracing itself.
link_kube_tracing() {
    remove_link_if_exists $tracing_link_dir
    mkdir -p $(dirname $tracing_link_dir)
    if [ ! -d $tracing_link_dir ]; then
        echo "link $tracing_src_dir to $tracing_link_dir"
        ln -sf $tracing_src_dir $tracing_link_dir
    fi
}

clear_git_dir() {
    for dir in `find $tracing_src_dir -name '.git' -type d`; do
        rm -rf $dir
    done
}

clear_glide_cache
fetch_kube_tracing_source_code
link_kube_tracing_dependencies
link_kube_tracing
clear_git_dir
