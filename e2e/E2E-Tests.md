Run `make test-e2e` on an existing cluster to test the current HEAD of
NHC with Self Node Remediation.

Prerequisite:
- cluster with at least 2 workers which are rebootable (k8s or OCP)
- available kubeconfig ($HOME/.kube/config or export KUBECONFIG if needed)
- container image of the current NHC version in a registry
  (openshift-ci does that as a dependency. manual invocation should
  use make docker-build docker-push)

Goals:
- Test end-to-end the HEAD of the current repo with Self Node Remediation
- Build, push and use Self Node Remediation image from its main branch

Non-Goals:
- Plugablle structure for new remediators

The order of actions is roughly:
- create a k8s client
- download latest self node remediation git repo
- make docker-build docker-push deploy for self node remediation
- make deploy NHC using current $IMG (where IMG is a based on current git HASH)
- run go tests to test behavior
  - fail a host, see its picked up NHC and SNR, watch the node come back healthy
  - fail a host which is not under NHC selector, see it's untouched

