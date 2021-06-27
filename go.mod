module github.com/medik8s/node-healthcheck-operator

go 1.16

require (
	github.com/go-logr/logr v0.3.0
	github.com/onsi/ginkgo v1.16.2
	github.com/onsi/gomega v1.10.2
	github.com/pkg/errors v0.9.1
	github.com/prometheus/client_golang v1.7.1
	golang.org/x/sys v0.0.0-20210320140829-1e4c9ba3b0c4 // indirect
	k8s.io/api v0.19.2
	k8s.io/apiextensions-apiserver v0.19.2
	k8s.io/apimachinery v0.19.2
	k8s.io/client-go v0.19.2
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800
	sigs.k8s.io/controller-runtime v0.7.0
)
