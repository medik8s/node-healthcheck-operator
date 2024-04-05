package cluster

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/pkg/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	v1 "github.com/openshift/api/config/v1"
	configv1 "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
)

var (
	versionRegex = regexp.MustCompile(`(\d+)\.(\d+)`)
)

type Capabilities struct {
	IsOnOpenshift, HasMachineAPI bool
}

type ServerVersion struct {
	OcpVersion   string
	Major, Minor int
}

func NewCapabilities(config *rest.Config) (Capabilities, error) {
	var err error
	var onOpenshift, machineAPI bool

	if onOpenshift, err = isOnOpenshift(config); err != nil {
		return Capabilities{}, errors.Wrap(err, "failed to check if cluster is on OpenShift")
	}

	if onOpenshift {
		if machineAPI, err = hasMachineAPI(config); err != nil {
			return Capabilities{}, errors.Wrap(err, "failed to check machine API capability")
		}
	}

	return Capabilities{
		IsOnOpenshift: onOpenshift,
		HasMachineAPI: machineAPI,
	}, nil
}

// isOnOpenshift returns true if the cluster has the openshift config group
func isOnOpenshift(config *rest.Config) (bool, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return false, err
	}
	apiGroups, err := dc.ServerGroups()
	kind := schema.GroupVersionKind{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"}
	for _, apiGroup := range apiGroups.Groups {
		for _, supportedVersion := range apiGroup.Versions {
			if supportedVersion.GroupVersion == kind.GroupVersion().String() {
				return true, nil
			}
		}
	}
	return false, nil
}

// hasMachineAPI returns true it the cluster has the MachineAPI
func hasMachineAPI(config *rest.Config) (bool, error) {
	client, err := configv1.NewForConfig(config)
	if err != nil {
		return false, errors.Wrapf(err, "failed to create cluster version client")
	}
	ocpVersion, err := getOpenshiftVersion(client)
	if err != nil {
		return false, errors.Wrap(err, "could not get OCP version")
	}

	if ocpVersion.Major == 4 && ocpVersion.Minor >= 14 {
		return hasMachineAPICapability(client)
	} else {
		return hasMachineAPIGroup(config)
	}
}

// getOpenshiftVersion returns the OpenShift version of the cluster
func getOpenshiftVersion(client *configv1.ConfigV1Client) (*ServerVersion, error) {
	clusterVersion, err := client.ClusterVersions().Get(context.TODO(), "version", metav1.GetOptions{})
	if err != nil {
		return nil, errors.Wrapf(err, "could not get OCP version")
	}

	var ocpVersion string
	for _, update := range clusterVersion.Status.History {
		if update.State == v1.CompletedUpdate {
			// obtain the version from the last completed update
			ocpVersion = update.Version
			break
		}
	}
	if ocpVersion == "" {
		return nil, errors.New("could not find up to date OCP version")
	}

	matches := versionRegex.FindStringSubmatch(ocpVersion)
	if matches == nil || len(matches) != 3 {
		return nil, fmt.Errorf("could not get OCP version from %s (found %d matches)", ocpVersion, matches)
	}
	major, err := strconv.Atoi(matches[1])
	if err != nil {
		return nil, errors.Wrap(err, "could not get major version")
	}
	minor, err := strconv.Atoi(matches[2])
	if err != nil {
		return nil, errors.Wrap(err, "could not get minor version")
	}

	return &ServerVersion{
		OcpVersion: ocpVersion,
		Major:      major,
		Minor:      minor,
	}, nil
}

// hasMachineAPICapability returns true if the cluster has the MachineAPI capability enabled
func hasMachineAPICapability(c *configv1.ConfigV1Client) (bool, error) {
	cvs, err := c.ClusterVersions().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, errors.Wrapf(err, "failed to get ClusterVersion")
	}

	for _, cv := range cvs.Items {
		if cv.Status.Capabilities.EnabledCapabilities == nil {
			return false, nil
		}
		var MachineAPI v1.ClusterVersionCapability = "MachineAPI"
		for _, capability := range cv.Status.Capabilities.EnabledCapabilities {
			if capability == MachineAPI {
				return true, nil
			}
		}
	}

	return false, nil
}

func hasMachineAPIGroup(c *rest.Config) (bool, error) {
	dc, err := discovery.NewDiscoveryClientForConfig(c)
	if err != nil {
		return false, err
	}
	apiGroups, err := dc.ServerGroups()
	group := schema.GroupVersion{Group: "machine.openshift.io", Version: "v1"}
	for _, apiGroup := range apiGroups.Groups {
		if apiGroup.Name == group.Group {
			for _, supportedVersion := range apiGroup.Versions {
				if supportedVersion.Version == group.Version {
					return true, nil
				}
			}
		}
	}

	return false, nil
}
