#!/usr/bin/env bash

export OPERATOR_NS=${OPERATOR_NS:-openshift-operators}
export SNRT_NAME=${SNRT_NAME:-self-node-remediation-automatic-strategy-template}

set -e

goTest() {
  (
    # don't stop on errors here only, and print full command
    set +e -x
    FILTER="${1}"
    if [[ -n "${LABEL_FILTER}" ]]; then
      FILTER="${FILTER} && ${LABEL_FILTER}"
    fi
    go test ./e2e -coverprofile cover.out -timeout 60m -test.v -ginkgo.vv -ginkgo.label-filter="${FILTER}" "${TEST_OPTS}"
  )
}

if [[ -n ${SNR_STRATEGY} ]]; then
  echo "Using SNR strategy: ${SNR_STRATEGY}"
  TEMPLATE_NAME=snr-${SNR_STRATEGY}-template
  # to lower case
  TEMPLATE_NAME=${TEMPLATE_NAME,,}
  export SNRT_NAME=${TEMPLATE_NAME}
  kubectl -n ${OPERATOR_NS} delete snrt ${SNRT_NAME} || true
  cat <<EOF | kubectl create -f -
apiVersion: self-node-remediation.medik8s.io/v1alpha1
kind: SelfNodeRemediationTemplate
metadata:
  name: ${TEMPLATE_NAME}
  namespace: ${OPERATOR_NS}
spec:
  template:
    spec:
      remediationStrategy: ${SNR_STRATEGY}
EOF
fi

# no colors in CI
if ! which tput &>/dev/null 2>&1 || [[ $(tput -T$TERM colors) -lt 8 ]] || [[ -n ${CI} ]] ; then
  echo "Terminal does not seem to support colored output, disabling it"
  TEST_OPTS="${TEST_OPTS} -ginkgo.no-color"
fi

exitCode=0

echo "Running NodeHealthCheck e2e tests"
goTest "NHC" || exitCode=$((exitCode+1))

if ! command -v oc &>/dev/null; then
  echo "skipping MHC test, oc binary not found in PATH"
  exit $exitCode
fi

echo "Preparing MachineHealthCheck e2e tests"

retry() {
  local retries=$1
  local wait=$2
  shift 2
  local n=0
  # The 'until' command repeatedly executes the given command(s) until they succeed (i.e., return a zero exit status).
  # In this context, "$@" represents the command and its arguments passed to the retry function.
  # The loop will continue to run the command until it succeeds or the maximum number of retries is reached.
  until "$@"; do
    n=$((n+1))
    if [ $n -ge $retries ]; then
      echo "Command failed after $retries attempts: $*"
      return 1
    fi
    echo "Retry $n/$retries failed. Retrying in $wait seconds..."
    sleep $wait
  done
}

echo "Pausing MachineConfigPools in order to prevent reboots after enabling feature gate"
retry 3 5 oc patch machineconfigpool worker --type=merge --patch='{"spec":{"paused":true}}'
retry 3 5 oc patch machineconfigpool master --type=merge --patch='{"spec":{"paused":true}}'
sleep 5

echo "Enabling MachineAPIOperatorDisableMachineHealthCheckController feature gate and waiting a bit to let machine-controllers redeploy."
echo "HEADS UP: This will disable OCP upgrades forever!"
retry 3 5 oc patch featuregate cluster --type merge --patch \
  '{"spec":{"featureSet": "CustomNoUpgrade","customNoUpgrade":{"enabled":["MachineAPIOperatorDisableMachineHealthCheckController"],"disabled":[]}}}'
sleep 15

SNRT_TARGET_NS=openshift-machine-api
retry 3 5 bash -c "
  oc get selfnoderemediationtemplates ${SNRT_NAME} --namespace ${SNRT_TARGET_NS} \
    || (echo 'Copying SNR template to ${SNRT_TARGET_NS}' \
      && oc get selfnoderemediationtemplates ${SNRT_NAME} --namespace=${OPERATOR_NS} -o yaml \
        | grep -v '^\s*namespace:\s' \
        | oc create --namespace=${SNRT_TARGET_NS} -f -)
"

echo "Running MachineHealthCheck e2e tests"
goTest "MHC" || exitCode=$((exitCode+1))

echo "Deleting SNR template in ${SNRT_TARGET_NS}" \
oc delete selfnoderemediationtemplates ${SNRT_NAME} --namespace ${SNRT_TARGET_NS}

# skip cleanup... makes it much easier to run tests locally multiple times
# leave it here though for documentation...

#echo "Disabling MachineAPIOperatorDisableMachineHealthCheckController feature gate"
#oc patch featuregate cluster --type merge --patch \
#  '{"spec":{"featureSet": "CustomNoUpgrade","customNoUpgrade":{"enabled":[],"disabled":["MachineAPIOperatorDisableMachineHealthCheckController"]}}}'
#sleep 15
#
#echo "Unpausing MachineConfigPool"
#oc patch machineconfigpool worker --type=merge --patch='{"spec":{"paused":false}}'
#oc patch machineconfigpool master --type=merge --patch='{"spec":{"paused":false}}'

exit $exitCode
