#!/usr/bin/env bash

set -eou pipefail

NAMESPACE="rapidast-nhc"
API_CLUSTER=$(grep "server: https://" $KUBECONFIG | sed -r 's#.+?//##' | head -1)
TOKEN=$(oc create token privileged-sa -n $NAMESPACE)
API_CLUSTER_NAME=$(echo $API_CLUSTER | cut -d ':' -f 1)
OAST_CALLBACK_PORT=$(python -c "import socket; s=socket.socket(); s.bind((\"\", 0)); print(s.getsockname()[1]); s.close()")
OAST_CALLBACK_ADDRESS=$(ip -o route get `(dig +short $API_CLUSTER_NAME)` | awk '{ print $3 }')

# Define the content for the ConfigMap
configmap_content=$(cat <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: rapidast-configmap
  namespace: ${NAMESPACE}
data:
  rapidastconfig.yaml: |
    config:
      configVersion: 4

    application:
      shortName: "nhc"
      url: "https://${API_CLUSTER}"

    general:
      authentication:
        type: "http_header"
        parameters:
          name: "Authorization"
          value: "Bearer ${TOKEN}"
      container:
        type: "none"

    scanners:
      zap:
        apiScan:
          apis:
            apiUrl: "https://$API_CLUSTER/openapi/v3/apis/remediation.medik8s.io/v1alpha1"
        activeScan:
          policy: "Operator-scan"
        miscOptions:
          enableUI: False
          updateAddons: False
          overrideConfigs:
            - formhandler.fields.field(0).fieldId=namespace
            - formhandler.fields.field(0).value=openshift-operators
            - oast.callback.port=$OAST_CALLBACK_PORT
            - oast.callback.remoteaddr=$OAST_CALLBACK_ADDRESS
EOF
)

# Create the ConfigMap
echo "$configmap_content" | oc -n ${NAMESPACE} create -f -
