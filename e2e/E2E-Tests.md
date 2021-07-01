Run `make test-e2e` on an existing cluster to test the current HEAD of
NHC with poison-pill.

Prerequisite:
 - cluster with at least 2 workers which are rebootable (k8s or OCP)
 - available kubeconfig ($HOME/.kube/config or export KUBECONFIG if needed)
 - container image of the current NHC version in a registry
   (openshift-ci does that as a dependency. manuall invocation should
   use make docker-build docker-push)

Goals:
 - Test end-to-end the HEAD of the current repo with Poison-Pill
 - Use Poison-Pill image that is pinned in its master branch. (can be
   changed using PPIL_GIT_REF)

Non-Goals:
 - Plugablle structure for new remediators

The order of actions is roughly:
 - create a k8s client
 - download latest poison pill git repo
 - make deploy poison pill
 - create a remediation template resource
 - make deploy NHC using current $IMG (where IMG is a based on current git HASH)
 - provision a basic NHC resource with PP resource reference
 - run go tests to test behavior
    - fail a host, see its picked up NHC and PP, watch the node come back healthy
    - fail a host which is not under NHC selector, see it's untouched

