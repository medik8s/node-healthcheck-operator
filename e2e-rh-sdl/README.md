# rapidast execution for node-healthcheck operator

This test is running [rapidast](https://github.com/RedHatProductSecurity/rapidast) scan targeting the api extension that the node-healthcheck operator
provides to the openshift api.

The test steps ensure the generation of the needed token to retrieve the api extension urls, customize the
rapidast config file, runs the scan in a docker instance and check for the results.

Follow these step to run manually the scan inside the container:

* Create the container image:

`cd test/e2e-rh-sdl`

`podman build . -t e2e-rh-sdl`

* Run the container (adapt the kubeconfig file path to your environment):

`podman run -it -v ~/clusterconfigs/auth/:/kube/config:Z --env KUBECONFIG=/kube/config/kubeconfig e2e-rh-sdl /bin/bash`

* Launch the scan using kuttl utility:

`ARTIFACT_DIR=/tmp KUBECONFIG=$KUBECONFIG kuttl test --timeout=300 --test=nhc rapidast/`
