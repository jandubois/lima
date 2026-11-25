#!/usr/bin/env bash

# SPDX-FileCopyrightText: Copyright The Lima Authors
# SPDX-License-Identifier: Apache-2.0

# DO NOT MERGE. Samples the host and pushes the samples and VM logs to the
# vmnet-evidence branch until the job ends, because a lost runner uploads nothing.
#
# Usage: vmnet-evidence.sh <job-name>

set -o nounset
shopt -s nullglob

name=$1
sampler=$PWD/hack/vmnet-sample.sh
cd "$HOME" || exit 1
mkdir -p vmnet-evidence/vm vmnet-evidence/reports
# Reports older than this marker come from the runner image, not from this job.
touch vmnet-evidence/job-start
nohup "$sampler" vmnet-evidence 5 >vmnet-evidence/sampler.log 2>&1 &

# An earlier run may have left this file, and replacing it needs its sha.
sha=$(gh api "repos/${GITHUB_REPOSITORY}/contents/evidence/${name}.tgz?ref=vmnet-evidence" --jq .sha 2>/dev/null) || sha=
while :; do
    # Keep a copy of these logs, because teardown deletes the instances.
    for dir in .lima-bats/*/; do
        instance=$(basename "$dir")
        mkdir -p "vmnet-evidence/vm/${instance}"
        for log in "$dir"serial*.log "$dir"ha.stderr*.log "$dir"*socket_vmnet*.log; do
            cp "$log" "vmnet-evidence/vm/${instance}/"
        done
    done
    # Copy the reports that macOS writes when a guest's XPC process crashes or uses too much
    # CPU, and the JetsamEvent report that it writes when it kills processes to free memory.
    # The system directory is mode 0770 for root and _analyticsusers, so the copy needs sudo.
    sudo find /Library/Logs/DiagnosticReports "$HOME/Library/Logs/DiagnosticReports" -type f \
        \( -name '*Virtualization*' -o -name 'JetsamEvent*' \) -newer vmnet-evidence/job-start \
        -exec cp {} vmnet-evidence/reports/ \; 2>/dev/null
    sudo chown -R "$(id -un)" vmnet-evidence/reports

    tar -czf vmnet-evidence.tgz vmnet-evidence
    base64 <vmnet-evidence.tgz >vmnet-evidence.b64
    jq --null-input --arg message "$name" --arg sha "$sha" --rawfile content vmnet-evidence.b64 \
        '{$message, $content, branch: "vmnet-evidence"} + (if $sha == "" then {} else {$sha} end)' \
        >vmnet-evidence.json
    if new=$(gh api --method PUT "repos/${GITHUB_REPOSITORY}/contents/evidence/${name}.tgz" \
        --input vmnet-evidence.json --jq .content.sha); then
        sha=$new
    fi
    sleep 30
done
