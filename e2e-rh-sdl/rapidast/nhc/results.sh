#!/usr/bin/env bash

set -eou pipefail

# Temp directory to store generated pod yaml
TMP_DIR=/tmp

# Where to store sync'd results -- defaults to current dir
ARTIFACT_DIR=${ARTIFACT_DIR}

# Name for rapiterm pod
RANDOM_NAME=rapiterm-$RANDOM

# Name of PVC in RapiDAST Resource, i.e. which PVC to mount to grab results
PVC=rapidast-pvc

IMAGE_REPOSITORY=quay.io/redhatproductsecurity/rapidast-term
IMAGE_TAG=latest
NAMESPACE=rapidast-nhc

cat <<EOF > $TMP_DIR/$RANDOM_NAME
apiVersion: v1
kind: Pod
metadata:
  name: $RANDOM_NAME
  namespace: $NAMESPACE
spec:
  containers:
    - name: terminal
      image: '$IMAGE_REPOSITORY:$IMAGE_TAG'
      command: ['sleep', '300']
      imagePullPolicy: Always
      volumeMounts:
        - name: results-volume
          mountPath: /zap/results/
      resources:
        limits:
          cpu: 100m
          memory: 500Mi
        requests:
          cpu: 50m
          memory: 100Mi
  volumes:
    - name: results-volume
      persistentVolumeClaim:
        claimName: $PVC
EOF

kubectl apply -f $TMP_DIR/$RANDOM_NAME
rm $TMP_DIR/$RANDOM_NAME
kubectl -n $NAMESPACE wait --for=condition=Ready pod/$RANDOM_NAME
kubectl -n $NAMESPACE cp $RANDOM_NAME:/zap/results $ARTIFACT_DIR

# Function to search for 'session' file and zap-report.json recursively
search_for_files() {
  local dir="$1/nhc"
  local found_session=0
  local found_zap_report=0

  while IFS= read -r -d '' file; do
    if [[ "$file" == *"session"* ]]; then
      found_session=1
    elif [[ "$file" == *"zap-report.json" ]]; then
      found_zap_report=1
    fi
  done < <(find "$dir" -type f \( -name "session*" -o -name "zap-report.json" \) -print0)

  if [[ "$found_session" -eq 0 || "$found_zap_report" -eq 0 ]]; then
    echo "Either 'session' file or 'zap-report.json' files not found in subdirectories of $dir, failing..."
    exit 1
  fi
}

# Search for 'session' file and zap-report.json in subdirectories of $ARTIFACT_DIR
search_for_files "$ARTIFACT_DIR"

