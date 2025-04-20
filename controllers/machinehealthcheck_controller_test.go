package controllers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	commonannotations "github.com/medik8s/common/pkg/annotations"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/storage/names"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	machinev1 "github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/node-healthcheck-operator/controllers/cluster"
	"github.com/medik8s/node-healthcheck-operator/controllers/featuregates"
	"github.com/medik8s/node-healthcheck-operator/controllers/mhc"
	"github.com/medik8s/node-healthcheck-operator/controllers/resources"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils/annotations"
)

const (
	// machineAnnotationKey contains default machine node annotation
	machineAnnotationKey = "machine.openshift.io/machine"
)

var (
	remediationAllowedCondition = machinev1.Condition{
		Type:   machinev1.RemediationAllowedCondition,
		Status: corev1.ConditionTrue,
	}

	// KnownDate contains date that can be used under tests
	KnownDate = metav1.Time{Time: time.Date(1985, 06, 03, 0, 0, 0, 0, time.Local)}
)

type testCase struct {
	name                string
	machine             *machinev1.Machine
	node                *corev1.Node
	mhc                 *machinev1.MachineHealthCheck
	expected            expectedReconcile
	expectedEvents      []string
	expectedStatus      *machinev1.MachineHealthCheckStatus
	remediationCR       *unstructured.Unstructured
	remediationTemplate *unstructured.Unstructured
}

type expectedReconcile struct {
	result reconcile.Result
	error  bool
}

func init() {
	// Add types to scheme
	if err := machinev1.Install(scheme.Scheme); err != nil {
		panic(err)
	}
}

func TestReconcile(t *testing.T) {
	ctx := context.Background()

	// healthy node
	nodeHealthy := newNodeForMHC("healthy", true)
	nodeHealthy.Annotations = map[string]string{
		machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machineWithNodehealthy"),
	}
	machineWithNodeHealthy := newMachine("machineWithNodehealthy", nodeHealthy.Name)

	// recently unhealthy node
	nodeRecentlyUnhealthy := newNodeForMHC("recentlyUnhealthy", false)
	nodeRecentlyUnhealthy.Status.Conditions[0].LastTransitionTime = metav1.Time{Time: time.Now()}
	nodeRecentlyUnhealthy.Annotations = map[string]string{
		machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machineWithNodeRecentlyUnhealthy"),
	}
	machineWithNodeRecentlyUnhealthy := newMachine("machineWithNodeRecentlyUnhealthy", nodeRecentlyUnhealthy.Name)

	// node without machine annotation
	nodeWithoutMachineAnnotation := newNodeForMHC("withoutMachineAnnotation", true)
	nodeWithoutMachineAnnotation.Annotations = map[string]string{}

	// node annotated with machine that does not exist
	nodeAnnotatedWithNoExistentMachine := newNodeForMHC("annotatedWithNoExistentMachine", true)
	nodeAnnotatedWithNoExistentMachine.Annotations[machineAnnotationKey] = "annotatedWithNoExistentMachine"

	// node annotated with machine without owner reference
	nodeAnnotatedWithMachineWithoutOwnerReference := newNodeForMHC("annotatedWithMachineWithoutOwnerReference", false)
	nodeAnnotatedWithMachineWithoutOwnerReference.Annotations = map[string]string{
		machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machineWithoutOwnerController"),
	}
	machineWithoutOwnerController := newMachine("machineWithoutOwnerController", nodeAnnotatedWithMachineWithoutOwnerReference.Name)
	machineWithoutOwnerController.OwnerReferences = nil

	// node annotated with machine without node reference
	nodeAnnotatedWithMachineWithoutNodeReference := newNodeForMHC("annotatedWithMachineWithoutNodeReference", true)
	nodeAnnotatedWithMachineWithoutNodeReference.Annotations = map[string]string{
		machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machineWithoutNodeRef"),
	}
	machineWithoutNodeRef := newMachine("machineWithoutNodeRef", nodeAnnotatedWithMachineWithoutNodeReference.Name)
	machineWithoutNodeRef.Status.NodeRef = nil

	machineHealthCheck := newMachineHealthCheck("machineHealthCheck", infraRemediationTemplateRef)
	nodeStartupTimeout := 15 * time.Minute
	machineHealthCheck.Spec.NodeStartupTimeout = &metav1.Duration{Duration: nodeStartupTimeout}

	machineHealthCheckNegativeMaxUnhealthy := newMachineHealthCheck("machineHealthCheckNegativeMaxUnhealthy", infraRemediationTemplateRef)
	negativeOne := intstr.FromInt(-1)
	machineHealthCheckNegativeMaxUnhealthy.Spec.MaxUnhealthy = &negativeOne
	machineHealthCheckNegativeMaxUnhealthy.Spec.NodeStartupTimeout = &metav1.Duration{Duration: nodeStartupTimeout}

	machineHealthCheckPaused := newMachineHealthCheck("machineHealthCheck", infraRemediationTemplateRef)
	machineHealthCheckPaused.Annotations = make(map[string]string)
	machineHealthCheckPaused.Annotations[annotations.MHCPausedAnnotation] = "test"

	// remediationExternal
	nodeUnhealthyForTooLong := newNodeForMHC("nodeUnhealthyForTooLong", false)
	nodeUnhealthyForTooLong.Annotations = map[string]string{
		machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machineUnhealthyForTooLong"),
	}
	machineUnhealthyForTooLong := newMachine("machineUnhealthyForTooLong", nodeUnhealthyForTooLong.Name)

	nodeAlreadyDeleted := newNodeForMHC("nodeAlreadyDelete", false)
	nodeAlreadyDeleted.Annotations = map[string]string{
		machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machineAlreadyDeleted"),
	}
	machineAlreadyDeleted := newMachine("machineAlreadyDeleted", nodeAlreadyDeleted.Name)
	machineAlreadyDeleted.SetDeletionTimestamp(&metav1.Time{Time: time.Now()})
	machineAlreadyDeleted.SetFinalizers([]string{"test"})

	testCases := []testCase{
		{
			name:    "machine unhealthy",
			machine: machineUnhealthyForTooLong,
			node:    nodeUnhealthyForTooLong,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{utils.EventReasonRemediationCreated},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine unhealthy, MHC paused",
			machine: machineUnhealthyForTooLong,
			node:    nodeUnhealthyForTooLong,
			mhc:     machineHealthCheckPaused,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{},
		},
		{
			name:    "machine with node healthy",
			machine: machineWithNodeHealthy,
			node:    nodeHealthy,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(1),
				RemediationsAllowed: 1,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine with node likely to go unhealthy",
			machine: machineWithNodeRecentlyUnhealthy,
			node:    nodeRecentlyUnhealthy,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{
					Requeue:      true,
					RequeueAfter: 300 * time.Second,
				},
				error: false,
			},
			expectedEvents: []string{utils.EventReasonDetectedUnhealthy},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "no target: no machine and bad node annotation",
			machine: nil,
			node:    nodeWithoutMachineAnnotation,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(0),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "no target: no machine",
			machine: nil,
			node:    nodeAnnotatedWithNoExistentMachine,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(0),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine no controller owner",
			machine: machineWithoutOwnerController,
			node:    nodeAnnotatedWithMachineWithoutOwnerReference,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{utils.EventReasonRemediationCreated},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine no noderef",
			machine: machineWithoutNodeRef,
			node:    nodeAnnotatedWithMachineWithoutNodeReference,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{
					RequeueAfter: nodeStartupTimeout,
				},
				error: false,
			},
			expectedEvents: []string{utils.EventReasonDetectedUnhealthy},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine already deleted",
			machine: machineAlreadyDeleted,
			node:    nodeAlreadyDeleted,
			mhc:     machineHealthCheck,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine healthy with MHC negative maxUnhealthy",
			machine: machineWithNodeHealthy,
			node:    nodeHealthy,
			mhc:     machineHealthCheckNegativeMaxUnhealthy,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(1),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
		{
			name:    "machine unhealthy with MHC negative maxUnhealthy",
			machine: machineUnhealthyForTooLong,
			node:    nodeUnhealthyForTooLong,
			mhc:     machineHealthCheckNegativeMaxUnhealthy,
			expected: expectedReconcile{
				result: reconcile.Result{
					Requeue: true,
				},
				error: false,
			},
			expectedEvents: []string{utils.EventReasonRemediationSkipped},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					{
						Type:     machinev1.RemediationAllowedCondition,
						Status:   corev1.ConditionFalse,
						Severity: machinev1.ConditionSeverityWarning,
						Reason:   machinev1.TooManyUnhealthyReason,
						Message:  "Remediation is not allowed, the number of not started or unhealthy machines exceeds maxUnhealthy (total: 1, unhealthy: 1, maxUnhealthy: -1)",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := record.NewFakeRecorder(2)
			r := newFakeReconcilerWithCustomRecorder(recorder, buildRunTimeObjects(tc)...)
			assertBaseReconcile(t, tc, ctx, r)
		})
	}
}

func TestReconcileExternalRemediationTemplate(t *testing.T) {
	ctx := context.Background()

	nodeHealthy := newNodeForMHC("NodeHealthy", true)
	machineWithNodeHealthy := newMachine("Machine", nodeHealthy.Name)

	nodeUnHealthy := newNodeForMHC("NodeUnhealthy", false)
	machineWithNodeUnHealthy := newMachine("Machine", nodeUnHealthy.Name)
	machineWithNodeUnHealthy.APIVersion = machinev1.SchemeGroupVersion.String()

	mhc := newMachineHealthCheck("machineHealthCheck", infraRemediationTemplateRef)
	mhcMultipleSupport := newMachineHealthCheck("machineHealthCheck", infraMultipleRemediationTemplateRef)

	remediationTemplateCR := newTestRemediationTemplateCR(InfraRemediationKind, MachineNamespace, InfraRemediationTemplateName)
	remediationMultipleSupportTemplateCR := newTestRemediationTemplateCR(InfraRemediationKind, MachineNamespace, InfraMultipleSupportRemediationTemplateName)
	owner := metav1.OwnerReference{
		APIVersion: mhc.APIVersion,
		Kind:       mhc.Kind,
		Name:       mhc.Name,
		UID:        mhc.UID,
	}
	remediationCR := newRemediationCR(machineWithNodeUnHealthy.Name, nodeUnHealthy.Name, *mhc.Spec.RemediationTemplate, owner)

	testCases := []testCase{

		{ //When remediationTemplate is set and node transitions back to healthy, new Remediation Request should be deleted
			name:                "external remediation is done",
			machine:             machineWithNodeHealthy,
			node:                nodeHealthy,
			mhc:                 mhc,
			remediationCR:       remediationCR,
			remediationTemplate: remediationTemplateCR,
			expected: expectedReconcile{
				result: reconcile.Result{Requeue: true},
				error:  false,
			},
			expectedEvents: []string{utils.EventReasonRemediationRemoved},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(1),
				RemediationsAllowed: 1,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},

		{ //When remediationTemplate is set and node transitions to unhealthy, new Remediation Request should be created
			name:                "create new external remediation",
			machine:             machineWithNodeUnHealthy,
			node:                nodeUnHealthy,
			mhc:                 mhc,
			remediationCR:       nil,
			remediationTemplate: remediationTemplateCR,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{utils.EventReasonRemediationCreated},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},

		{ //When remediationTemplate is set and node transitions to unhealthy, and Remediation Request already exist
			name:                "external remediation is in process",
			machine:             machineWithNodeUnHealthy,
			node:                nodeUnHealthy,
			mhc:                 mhc,
			remediationCR:       remediationCR,
			remediationTemplate: remediationTemplateCR,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},

		{
			name:                "create new multiple template supported remediation",
			machine:             machineWithNodeUnHealthy,
			node:                nodeUnHealthy,
			mhc:                 mhcMultipleSupport,
			remediationCR:       nil,
			remediationTemplate: remediationMultipleSupportTemplateCR,
			expected: expectedReconcile{
				result: reconcile.Result{},
				error:  false,
			},
			expectedEvents: []string{utils.EventReasonRemediationCreated},
			expectedStatus: &machinev1.MachineHealthCheckStatus{
				ExpectedMachines:    pointer.Int(1),
				CurrentHealthy:      pointer.Int(0),
				RemediationsAllowed: 0,
				Conditions: machinev1.Conditions{
					remediationAllowedCondition,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := record.NewFakeRecorder(2)
			r := newFakeReconcilerWithCustomRecorder(recorder, buildRunTimeObjects(tc)...)
			assertBaseReconcile(t, tc, ctx, r)
			assertExternalRemediation(t, tc, ctx, r)

		})
	}
}

func TestMHCRequestsFromMachine(t *testing.T) {
	testCases := []struct {
		testCase         string
		mhcs             []*machinev1.MachineHealthCheck
		machine          *machinev1.Machine
		expectedRequests []reconcile.Request
	}{
		{
			testCase: "at least one match",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "match",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "noMatch",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine: newMachine("test", "node1"),
			expectedRequests: []reconcile.Request{
				{
					NamespacedName: client.ObjectKey{
						Namespace: MachineNamespace,
						Name:      "match",
					},
				},
			},
		},
		{
			testCase: "more than one match",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "match1",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "match2",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine: newMachine("test", "node1"),
			expectedRequests: []reconcile.Request{
				{
					NamespacedName: client.ObjectKey{
						Namespace: MachineNamespace,
						Name:      "match1",
					},
				},
				{
					NamespacedName: client.ObjectKey{
						Namespace: MachineNamespace,
						Name:      "match2",
					},
				},
			},
		},
		{
			testCase: "no match",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "noMatch1",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "noMatch2",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine:          newMachine("test", "node1"),
			expectedRequests: []ctrl.Request{},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			var objects []runtime.Object
			objects = append(objects, runtime.Object(tc.machine))
			for i := range tc.mhcs {
				objects = append(objects, runtime.Object(tc.mhcs[i]))
			}

			reconciler := newFakeReconciler(objects...)
			fg := &featuregates.FakeAccessor{IsMaoMhcDisabled: true}
			requests := utils.MHCByMachineMapperFunc(*reconciler, reconciler.Log, fg)(context.TODO(), tc.machine)
			if !reflect.DeepEqual(requests, tc.expectedRequests) {
				t.Errorf("Expected: %v, got: %v", tc.expectedRequests, requests)
			}
		})
	}
}

func TestMHCRequestsFromNode(t *testing.T) {
	testCases := []struct {
		testCase         string
		mhcs             []*machinev1.MachineHealthCheck
		node             *corev1.Node
		machine          *machinev1.Machine
		expectedRequests []reconcile.Request
	}{
		{
			testCase: "at least one match",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "match",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "noMatch",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine: newMachine("fakeMachine", "node1"),
			node:    newNodeForMHC("node1", true),
			expectedRequests: []reconcile.Request{
				{
					NamespacedName: client.ObjectKey{
						Namespace: MachineNamespace,
						Name:      "match",
					},
				},
			},
		},
		{
			testCase: "more than one match",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "match1",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "match2",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine: newMachine("fakeMachine", "node1"),
			node:    newNodeForMHC("node1", true),
			expectedRequests: []reconcile.Request{
				{
					NamespacedName: client.ObjectKey{
						Namespace: MachineNamespace,
						Name:      "match1",
					},
				},
				{
					NamespacedName: client.ObjectKey{
						Namespace: MachineNamespace,
						Name:      "match2",
					},
				},
			},
		},
		{
			testCase: "no match",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "noMatch1",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "noMatch2",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine:          newMachine("fakeMachine", "node1"),
			node:             newNodeForMHC("node1", true),
			expectedRequests: []ctrl.Request{},
		},
		{
			testCase: "node has bad machine annotation",
			mhcs: []*machinev1.MachineHealthCheck{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "mhc1",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"no": "match",
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			machine:          newMachine("noNodeAnnotation", "node1"),
			node:             newNodeForMHC("node1", true),
			expectedRequests: []ctrl.Request{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			var objects []runtime.Object
			objects = append(objects, runtime.Object(tc.machine), runtime.Object(tc.node))
			for i := range tc.mhcs {
				objects = append(objects, runtime.Object(tc.mhcs[i]))
			}

			reconciler := newFakeReconciler(objects...)
			fg := &featuregates.FakeAccessor{IsMaoMhcDisabled: true}
			requests := utils.MHCByNodeMapperFunc(*reconciler, reconciler.Log, fg)(context.TODO(), tc.node)

			if !reflect.DeepEqual(requests, tc.expectedRequests) {
				t.Errorf("Expected: %v, got: %v", tc.expectedRequests, requests)
			}
		})
	}
}

func TestGetTargetsFromMHC(t *testing.T) {
	machine1 := newMachine("match1", "node1")
	machine2 := newMachine("match2", "node2")
	mhc := newMachineHealthCheck("findTargets", infraRemediationTemplateRef)
	testCases := []struct {
		testCase        string
		mhc             *machinev1.MachineHealthCheck
		machines        []*machinev1.Machine
		nodes           []*corev1.Node
		expectedTargets []resources.Target
		expectedError   bool
	}{
		{
			testCase: "at least one match",
			mhc:      mhc,
			machines: []*machinev1.Machine{
				machine1,
				{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "noMatch",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"no": "match"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec: machinev1.MachineSpec{},
				},
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node1",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match1"),
						},
						Labels: map[string]string{},
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Node",
						APIVersion: "v1",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node2",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match2"),
						},
						Labels: map[string]string{},
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "Node",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{},
					},
				},
			},
			expectedTargets: []resources.Target{
				{
					MHC:     mhc,
					Machine: machine1,
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match1"),
							},
							Labels: map[string]string{},
							// the following line is to account for a change in the fake client, see https://github.com/kubernetes-sigs/controller-runtime/pull/1306
							ResourceVersion: "999",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{},
						},
					},
				},
			},
		},
		{
			testCase: "more than one match",
			mhc:      mhc,
			machines: []*machinev1.Machine{
				machine1,
				machine2,
			},
			nodes: []*corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node1",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match1"),
						},
						Labels: map[string]string{},
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Node",
						APIVersion: "v1",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node2",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match2"),
						},
						Labels: map[string]string{},
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Node",
						APIVersion: "v1",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{},
					},
				},
			},
			expectedTargets: []resources.Target{
				{
					MHC:     mhc,
					Machine: machine1,
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match1"),
							},
							Labels: map[string]string{},
							// the following line is to account for a change in the fake client, see https://github.com/kubernetes-sigs/controller-runtime/pull/1306
							ResourceVersion: "999",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{},
						},
					},
				},
				{
					MHC:     mhc,
					Machine: machine2,
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node2",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "match2"),
							},
							Labels: map[string]string{},
							// the following line is to account for a change in the fake client, see https://github.com/kubernetes-sigs/controller-runtime/pull/1306
							ResourceVersion: "999",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{},
						},
					},
				},
			},
		},
		{
			testCase: "machine has no node",
			mhc:      mhc,
			machines: []*machinev1.Machine{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "noNodeRef",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec:   machinev1.MachineSpec{},
					Status: machinev1.MachineStatus{},
				},
			},
			nodes: []*corev1.Node{},
			expectedTargets: []resources.Target{
				{
					MHC: mhc,
					Machine: &machinev1.Machine{
						TypeMeta: metav1.TypeMeta{Kind: "Machine"},
						ObjectMeta: metav1.ObjectMeta{
							Annotations:     make(map[string]string),
							Name:            "noNodeRef",
							Namespace:       MachineNamespace,
							Labels:          map[string]string{"foo": "bar"},
							OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
							// the following line is to account for a change in the fake client, see https://github.com/kubernetes-sigs/controller-runtime/pull/1306
							ResourceVersion: "999",
						},
						Spec:   machinev1.MachineSpec{},
						Status: machinev1.MachineStatus{},
					},
					Node: nil,
				},
			},
		},
		{
			testCase: "node not found",
			mhc:      mhc,
			machines: []*machinev1.Machine{
				machine1,
			},
			nodes: []*corev1.Node{},
			expectedTargets: []resources.Target{
				{
					MHC:     mhc,
					Machine: machine1,
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node1",
							Namespace: metav1.NamespaceNone,
						},
					},
				},
			},
		},
	}
	for _, tc := range testCases {
		var objects []runtime.Object
		objects = append(objects, runtime.Object(tc.mhc))
		for i := range tc.machines {
			objects = append(objects, runtime.Object(tc.machines[i]))
		}
		for i := range tc.nodes {
			objects = append(objects, runtime.Object(tc.nodes[i]))
		}

		t.Run(tc.testCase, func(t *testing.T) {
			reconciler := newFakeReconciler(objects...)
			leaseManager, _ := resources.NewLeaseManager(reconciler.Client, "test", reconciler.Log)
			recorder := record.NewFakeRecorder(2)
			rm := resources.NewManager(reconciler, ctx, reconciler.Log, true, leaseManager, recorder)
			got, err := rm.GetMHCTargets(tc.mhc)
			if !equality.Semantic.DeepEqual(got, tc.expectedTargets) {
				t.Errorf("Case: %v. Got: %+v, expected: %+v", tc.testCase, got, tc.expectedTargets)
			}
			if tc.expectedError != (err != nil) {
				t.Errorf("Case: %v. Got: %v, expected error: %v", tc.testCase, err, tc.expectedError)
			}
		})
	}
}

func TestNeedsRemediation(t *testing.T) {
	knownDate := metav1.Time{Time: time.Date(1985, 06, 03, 0, 0, 0, 0, time.Local)}
	machineFailed := machinev1.PhaseFailed
	testCases := []struct {
		testCase                    string
		target                      *resources.Target
		timeoutForMachineToHaveNode time.Duration
		expectedNeedsRemediation    bool
		expectedNextCheck           time.Duration
		expectedError               bool
	}{
		{
			testCase: "healthy: does not met conditions criteria",
			target: &resources.Target{
				Machine: newMachine("test", "node"),
				Node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
						},
						Labels: map[string]string{},
						UID:    "uid",
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Node",
						APIVersion: "v1",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{
								Type:               corev1.NodeReady,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: knownDate,
							},
						},
					},
				},
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			expectedNeedsRemediation:    false,
			expectedNextCheck:           time.Duration(0),
			expectedError:               false,
		},
		{
			testCase: "unhealthy: node does not exist",
			target: &resources.Target{
				Machine: newMachine("test", "node"),
				Node:    &corev1.Node{},
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			expectedNeedsRemediation:    true,
			expectedNextCheck:           time.Duration(0),
			expectedError:               false,
		},
		{
			testCase: "unhealthy: nodeRef nil longer than timeout",
			target: &resources.Target{
				Machine: &machinev1.Machine{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "machine",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec: machinev1.MachineSpec{},
					Status: machinev1.MachineStatus{
						LastUpdated: &metav1.Time{Time: time.Now().Add(-defaultNodeStartupTimeout - 1*time.Second)},
					},
				},
				Node: nil,
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			expectedNeedsRemediation:    true,
			expectedNextCheck:           time.Duration(0),
			expectedError:               false,
		},
		{
			testCase: "unhealthy: nodeRef nil, timeout disabled",
			target: &resources.Target{
				Machine: &machinev1.Machine{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "machine",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec: machinev1.MachineSpec{},
					Status: machinev1.MachineStatus{
						LastUpdated: &metav1.Time{Time: time.Now().Add(time.Duration(-defaultNodeStartupTimeout) - 1*time.Second)},
					},
				},
				Node: nil,
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 3600 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 3600 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: time.Duration(0),
			expectedNeedsRemediation:    false,
			expectedNextCheck:           time.Duration(0),
			expectedError:               false,
		},
		{
			testCase: "unhealthy: meet conditions criteria",
			target: &resources.Target{
				Machine: &machinev1.Machine{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "machine",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec: machinev1.MachineSpec{},
					Status: machinev1.MachineStatus{
						LastUpdated: &metav1.Time{Time: time.Now().Add(-defaultNodeStartupTimeout - 1*time.Second)},
					},
				},
				Node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
						},
						Labels: map[string]string{},
						UID:    "uid",
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Node",
						APIVersion: "v1",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{
								Type:               corev1.NodeReady,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(time.Duration(-400) * time.Second)},
							},
						},
					},
				},
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			expectedNeedsRemediation:    true,
			expectedNextCheck:           time.Duration(0),
			expectedError:               false,
		},
		{
			testCase: "unhealthy: machine phase failed",
			target: &resources.Target{
				Machine: &machinev1.Machine{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "machine",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec: machinev1.MachineSpec{},
					Status: machinev1.MachineStatus{
						Phase: &machineFailed,
					},
				},
				Node: nil,
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			expectedNeedsRemediation:    true,
			expectedNextCheck:           time.Duration(0),
			expectedError:               false,
		},
		{
			testCase: "healthy: meet conditions criteria but timeout",
			target: &resources.Target{
				Machine: &machinev1.Machine{
					TypeMeta: metav1.TypeMeta{Kind: "Machine"},
					ObjectMeta: metav1.ObjectMeta{
						Annotations:     make(map[string]string),
						Name:            "machine",
						Namespace:       MachineNamespace,
						Labels:          map[string]string{"foo": "bar"},
						OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
					},
					Spec: machinev1.MachineSpec{},
					Status: machinev1.MachineStatus{
						LastUpdated: &metav1.Time{Time: time.Now().Add(-defaultNodeStartupTimeout - 1*time.Second)},
					},
				},
				Node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "node",
						Namespace: metav1.NamespaceNone,
						Annotations: map[string]string{
							machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
						},
						Labels: map[string]string{},
						UID:    "uid",
					},
					TypeMeta: metav1.TypeMeta{
						Kind:       "Node",
						APIVersion: "v1",
					},
					Status: corev1.NodeStatus{
						Conditions: []corev1.NodeCondition{
							{
								Type:               corev1.NodeReady,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(time.Duration(-200) * time.Second)},
							},
						},
					},
				},
				MHC: &machinev1.MachineHealthCheck{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: MachineNamespace,
					},
					TypeMeta: metav1.TypeMeta{
						Kind: "MachineHealthCheck",
					},
					Spec: machinev1.MachineHealthCheckSpec{
						Selector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						UnhealthyConditions: []machinev1.UnhealthyCondition{
							{
								Type:    "Ready",
								Status:  "Unknown",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
							{
								Type:    "Ready",
								Status:  "False",
								Timeout: metav1.Duration{Duration: 300 * time.Second},
							},
						},
					},
					Status: machinev1.MachineHealthCheckStatus{},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			expectedNeedsRemediation:    false,
			expectedNextCheck:           1 * time.Minute, // 300-200 rounded
			expectedError:               false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			reconciler := newFakeReconciler()
			tc.target.MHC.Spec.NodeStartupTimeout = &metav1.Duration{Duration: tc.timeoutForMachineToHaveNode}
			needsRemediation, nextCheck, err := reconciler.needsRemediation(*tc.target)
			if needsRemediation != tc.expectedNeedsRemediation {
				t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, needsRemediation, tc.expectedNeedsRemediation)
			}
			if tc.expectedNextCheck == time.Duration(0) {
				if nextCheck != tc.expectedNextCheck {
					t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, int(nextCheck), int(tc.expectedNextCheck))
				}
			}
			if tc.expectedNextCheck != time.Duration(0) {
				now := time.Now()
				// since isUnhealthy will check timeout against now() again, the nextCheck must be slightly lower to the
				// margin calculated here
				if now.Add(nextCheck).Before(now.Add(tc.expectedNextCheck)) {
					t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, nextCheck, tc.expectedNextCheck)
				}
			}
			if tc.expectedError != (err != nil) {
				t.Errorf("Case: %v. Got: %v, expected error: %v", tc.testCase, err, tc.expectedError)
			}
		})
	}
}

func TestReconcileStatus(t *testing.T) {
	testCases := []struct {
		testCase            string
		mhc                 *machinev1.MachineHealthCheck
		totalTargets        int
		currentHealthy      int
		remediationsAllowed int32
	}{
		{
			testCase: "status gets new values",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{},
			},
			totalTargets:        10,
			currentHealthy:      5,
			remediationsAllowed: 5,
		},
		{
			testCase: "when the unhealthy machines exceed maxUnhealthy",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{},
			},
			totalTargets:        10,
			currentHealthy:      5,
			remediationsAllowed: 0,
		},
		{
			testCase: "when the unhealthy machines does not exceed maxUnhealthy",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{},
			},
			totalTargets:        10,
			currentHealthy:      7,
			remediationsAllowed: 1,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			tc.mhc.Status.ExpectedMachines = &tc.totalTargets
			tc.mhc.Status.CurrentHealthy = &tc.currentHealthy
			maxUnhealthy, _ := getMaxUnhealthy(tc.mhc, ctrl.Log)
			remAllowed := int32(getNrRemediationsAllowed(tc.totalTargets-tc.currentHealthy, maxUnhealthy))
			if remAllowed != tc.remediationsAllowed {
				t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, remAllowed, tc.remediationsAllowed)
			}
		})
	}
}

func TestHealthCheckTargets(t *testing.T) {
	now := time.Now()
	testCases := []struct {
		testCase                    string
		targets                     []resources.Target
		timeoutForMachineToHaveNode time.Duration
		currentHealthy              int
		needRemediationTargets      []resources.Target
		nextCheckTimesLen           int
		errList                     []error
	}{
		{
			testCase: "one healthy, one unhealthy",
			targets: []resources.Target{
				{
					Machine: &machinev1.Machine{
						TypeMeta: metav1.TypeMeta{Kind: "Machine"},
						ObjectMeta: metav1.ObjectMeta{
							Annotations:     make(map[string]string),
							Name:            "machine",
							Namespace:       MachineNamespace,
							Labels:          map[string]string{"foo": "bar"},
							OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
						},
						Spec:   machinev1.MachineSpec{},
						Status: machinev1.MachineStatus{},
					},
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
							},
							Labels: map[string]string{},
							UID:    "uid",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{
								{
									Type:               corev1.NodeReady,
									Status:             corev1.ConditionTrue,
									LastTransitionTime: metav1.Time{},
								},
							},
						},
					},
					MHC: &machinev1.MachineHealthCheck{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: MachineNamespace,
						},
						TypeMeta: metav1.TypeMeta{
							Kind: "MachineHealthCheck",
						},
						Spec: machinev1.MachineHealthCheckSpec{
							Selector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
							UnhealthyConditions: []machinev1.UnhealthyCondition{
								{
									Type:    "Ready",
									Status:  "Unknown",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
								{
									Type:    "Ready",
									Status:  "False",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
							},
							RemediationTemplate: infraRemediationTemplateRef,
						},
						Status: machinev1.MachineHealthCheckStatus{},
					},
				},
				{
					Machine: &machinev1.Machine{
						TypeMeta: metav1.TypeMeta{Kind: "Machine"},
						ObjectMeta: metav1.ObjectMeta{
							Annotations:     make(map[string]string),
							Name:            "machine",
							Namespace:       MachineNamespace,
							Labels:          map[string]string{"foo": "bar"},
							OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
						},
						Spec:   machinev1.MachineSpec{},
						Status: machinev1.MachineStatus{},
					},
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
							},
							Labels: map[string]string{},
							UID:    "uid",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{
								{
									Type:               corev1.NodeReady,
									Status:             corev1.ConditionFalse,
									LastTransitionTime: metav1.Time{Time: now.Add(time.Duration(-400) * time.Second)},
								},
							},
						},
					},
					MHC: &machinev1.MachineHealthCheck{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: MachineNamespace,
						},
						TypeMeta: metav1.TypeMeta{
							Kind: "MachineHealthCheck",
						},
						Spec: machinev1.MachineHealthCheckSpec{
							Selector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
							UnhealthyConditions: []machinev1.UnhealthyCondition{
								{
									Type:    "Ready",
									Status:  "Unknown",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
								{
									Type:    "Ready",
									Status:  "False",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
							},
							RemediationTemplate: infraRemediationTemplateRef,
						},
						Status: machinev1.MachineHealthCheckStatus{},
					},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			currentHealthy:              1,
			needRemediationTargets: []resources.Target{
				{
					Machine: &machinev1.Machine{
						TypeMeta: metav1.TypeMeta{Kind: "Machine"},
						ObjectMeta: metav1.ObjectMeta{
							Annotations:     make(map[string]string),
							Name:            "machine",
							Namespace:       MachineNamespace,
							Labels:          map[string]string{"foo": "bar"},
							OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
						},
						Spec:   machinev1.MachineSpec{},
						Status: machinev1.MachineStatus{},
					},
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
							},
							Labels: map[string]string{},
							UID:    "uid",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{
								{
									Type:               corev1.NodeReady,
									Status:             corev1.ConditionFalse,
									LastTransitionTime: metav1.Time{Time: now.Add(time.Duration(-400) * time.Second)},
								},
							},
						},
					},
					MHC: &machinev1.MachineHealthCheck{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: MachineNamespace,
						},
						TypeMeta: metav1.TypeMeta{
							Kind: "MachineHealthCheck",
						},
						Spec: machinev1.MachineHealthCheckSpec{
							Selector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
							UnhealthyConditions: []machinev1.UnhealthyCondition{
								{
									Type:    "Ready",
									Status:  "Unknown",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
								{
									Type:    "Ready",
									Status:  "False",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
							},
							RemediationTemplate: infraRemediationTemplateRef,
						},
						Status: machinev1.MachineHealthCheckStatus{},
					},
				},
			},
			nextCheckTimesLen: 0,
			errList:           []error{},
		},
		{
			testCase: "two checkTimes",
			targets: []resources.Target{
				{
					Machine: &machinev1.Machine{
						TypeMeta: metav1.TypeMeta{Kind: "Machine"},
						ObjectMeta: metav1.ObjectMeta{
							Annotations:     make(map[string]string),
							Name:            "machine",
							Namespace:       MachineNamespace,
							Labels:          map[string]string{"foo": "bar"},
							OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
						},
						Spec: machinev1.MachineSpec{},
						Status: machinev1.MachineStatus{
							LastUpdated: &metav1.Time{Time: now.Add(-defaultNodeStartupTimeout + 1*time.Minute)},
						},
					},
					Node: nil,
					MHC: &machinev1.MachineHealthCheck{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: MachineNamespace,
						},
						TypeMeta: metav1.TypeMeta{
							Kind: "MachineHealthCheck",
						},
						Spec: machinev1.MachineHealthCheckSpec{
							Selector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
							UnhealthyConditions: []machinev1.UnhealthyCondition{
								{
									Type:    "Ready",
									Status:  "Unknown",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
								{
									Type:    "Ready",
									Status:  "False",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
							},
							RemediationTemplate: infraRemediationTemplateRef,
						},
						Status: machinev1.MachineHealthCheckStatus{},
					},
				},
				{
					Machine: &machinev1.Machine{
						TypeMeta: metav1.TypeMeta{Kind: "Machine"},
						ObjectMeta: metav1.ObjectMeta{
							Annotations:     make(map[string]string),
							Name:            "machine",
							Namespace:       MachineNamespace,
							Labels:          map[string]string{"foo": "bar"},
							OwnerReferences: []metav1.OwnerReference{{Kind: "MachineSet"}},
						},
						Spec:   machinev1.MachineSpec{},
						Status: machinev1.MachineStatus{},
					},
					Node: &corev1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "node",
							Namespace: metav1.NamespaceNone,
							Annotations: map[string]string{
								machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "machine"),
							},
							Labels: map[string]string{},
							UID:    "uid",
						},
						TypeMeta: metav1.TypeMeta{
							Kind:       "Node",
							APIVersion: "v1",
						},
						Status: corev1.NodeStatus{
							Conditions: []corev1.NodeCondition{
								{
									Type:               corev1.NodeReady,
									Status:             corev1.ConditionFalse,
									LastTransitionTime: metav1.Time{Time: now.Add(time.Duration(-200) * time.Second)},
								},
							},
						},
					},
					MHC: &machinev1.MachineHealthCheck{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: MachineNamespace,
							UID:       "1234",
						},
						TypeMeta: metav1.TypeMeta{
							Kind: "MachineHealthCheck",
						},
						Spec: machinev1.MachineHealthCheckSpec{
							Selector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"foo": "bar",
								},
							},
							UnhealthyConditions: []machinev1.UnhealthyCondition{
								{
									Type:    "Ready",
									Status:  "Unknown",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
								{
									Type:    "Ready",
									Status:  "False",
									Timeout: metav1.Duration{Duration: 300 * time.Second},
								},
							},
							RemediationTemplate: infraRemediationTemplateRef,
						},
						Status: machinev1.MachineHealthCheckStatus{},
					},
				},
			},
			timeoutForMachineToHaveNode: defaultNodeStartupTimeout,
			currentHealthy:              0,
			nextCheckTimesLen:           2,
			errList:                     []error{},
		},
	}
	for _, tc := range testCases {
		recorder := record.NewFakeRecorder(2)
		r := newFakeReconcilerWithCustomRecorder(recorder)
		t.Run(tc.testCase, func(t *testing.T) {
			currentHealhty, needRemediationTargets, _, errList := r.checkHealth(tc.targets)
			if len(currentHealhty) != tc.currentHealthy {
				t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, currentHealhty, tc.currentHealthy)
			}
			if !equality.Semantic.DeepEqual(needRemediationTargets, tc.needRemediationTargets) {
				t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, needRemediationTargets, tc.needRemediationTargets)
			}
			//if len(nextCheckTimes) != tc.nextCheckTimesLen {
			//	t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, len(nextCheckTimes), tc.nextCheckTimesLen)
			//}
			if !equality.Semantic.DeepEqual(errList, tc.errList) {
				t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, errList, tc.errList)
			}
		})
	}
}

func TestIsAllowedRemediation(t *testing.T) {
	// short circuit if ever more than 2 out of 5 go unhealthy
	maxUnhealthyInt := intstr.FromInt(2)
	maxUnhealthyNegative := intstr.FromInt(-2)
	maxUnhealthyString := intstr.FromString("40%")
	maxUnhealthyIntInString := intstr.FromString("2")
	maxUnhealthyMixedString := intstr.FromString("foo%50")

	testCases := []struct {
		testCase string
		mhc      *machinev1.MachineHealthCheck
		expected bool
	}{
		{
			testCase: "not above maxUnhealthy",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyInt,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(3),
				},
			},
			expected: true,
		},
		{
			testCase: "above maxUnhealthy",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyInt,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(2),
				},
			},
			expected: false,
		},
		{
			testCase: "maxUnhealthy is negative",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyNegative,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(2),
				},
			},
			expected: false,
		},
		{
			testCase: "not above maxUnhealthy (percentage)",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyString,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(3),
				},
			},
			expected: true,
		},
		{
			testCase: "above maxUnhealthy (percentage)",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyString,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(2),
				},
			},
			expected: false,
		},
		{
			testCase: "not above maxUnhealthy (int in string)",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyIntInString,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(3),
				},
			},
			expected: true,
		},
		{
			testCase: "above maxUnhealthy (int in string)",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyIntInString,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: pointer.Int(5),
					CurrentHealthy:   pointer.Int(2),
				},
			},
			expected: false,
		},
		{
			testCase: "nil values",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyString,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: nil,
					CurrentHealthy:   nil,
				},
			},
			expected: true,
		},
		{
			testCase: "invalid string value",
			mhc: &machinev1.MachineHealthCheck{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: MachineNamespace,
					UID:       "1234",
				},
				TypeMeta: metav1.TypeMeta{
					Kind: "MachineHealthCheck",
				},
				Spec: machinev1.MachineHealthCheckSpec{
					Selector:            metav1.LabelSelector{},
					MaxUnhealthy:        &maxUnhealthyMixedString,
					RemediationTemplate: infraRemediationTemplateRef,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: nil,
					CurrentHealthy:   nil,
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.testCase, func(t *testing.T) {
			log := ctrl.Log
			maxUnhealthy, err := getMaxUnhealthy(tc.mhc, log)
			if err != nil {
				if tc.testCase != "invalid string value" {
					t.Errorf("Case: %v, failed to getMaxUnhealthy: %v", tc.testCase, err)
				}
				// expected
				return
			}
			unhealthy := pointer.IntDeref(tc.mhc.Status.ExpectedMachines, 0) - pointer.IntDeref(tc.mhc.Status.CurrentHealthy, 0)
			if got := isRemediationsAllowed(unhealthy, maxUnhealthy); got != tc.expected {
				t.Errorf("Case: %v. Got: %v, expected: %v", tc.testCase, got, tc.expected)
			}
		})
	}
}

func TestGetMaxUnhealthy(t *testing.T) {
	testCases := []struct {
		name                 string
		maxUnhealthy         *intstr.IntOrString
		expectedMaxUnhealthy int
		expectedMachines     int
		expectedErr          error
	}{
		{
			name:                 "when maxUnhealthy is nil",
			maxUnhealthy:         nil,
			expectedMaxUnhealthy: 7,
			expectedMachines:     7,
			expectedErr:          nil,
		},
		{
			name:                 "when maxUnhealthy is not an int or percentage",
			maxUnhealthy:         &intstr.IntOrString{Type: intstr.String, StrVal: "abcdef"},
			expectedMaxUnhealthy: 0,
			expectedMachines:     3,
			expectedErr:          errors.New("invalid value for IntOrString: invalid value \"abcdef\": strconv.Atoi: parsing \"abcdef\": invalid syntax"),
		},
		{
			name:                 "when maxUnhealthy is an int",
			maxUnhealthy:         &intstr.IntOrString{Type: intstr.Int, IntVal: 3},
			expectedMachines:     2,
			expectedMaxUnhealthy: 3,
			expectedErr:          nil,
		},
		{
			name:                 "when maxUnhealthy is a 40% (of 5)",
			maxUnhealthy:         &intstr.IntOrString{Type: intstr.String, StrVal: "40%"},
			expectedMachines:     5,
			expectedMaxUnhealthy: 2,
			expectedErr:          nil,
		},
		{
			name:                 "when maxUnhealthy is a 60% (of 7)",
			maxUnhealthy:         &intstr.IntOrString{Type: intstr.String, StrVal: "60%"},
			expectedMachines:     7,
			expectedMaxUnhealthy: 4,
			expectedErr:          nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			mhc := &machinev1.MachineHealthCheck{
				Spec: machinev1.MachineHealthCheckSpec{
					MaxUnhealthy: tc.maxUnhealthy,
				},
				Status: machinev1.MachineHealthCheckStatus{
					ExpectedMachines: &tc.expectedMachines,
				},
			}

			maxUnhealthy, err := getMaxUnhealthy(mhc, ctrl.Log)
			if tc.expectedErr != nil {
				g.Expect(err).To(Equal(tc.expectedErr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(maxUnhealthy).To(Equal(tc.expectedMaxUnhealthy))
		})
	}
}

func assertEvents(t *testing.T, testCase string, expectedEvents []string, realEvents chan string) {
	if len(expectedEvents) != len(realEvents) {
		realEv := []string{}
		close(realEvents)
		for ev := range realEvents {
			realEv = append(realEv, ev)
		}
		t.Errorf(
			"Test case: %s. Number of expected events (%+v) differs from number of real events (%+v)",
			testCase, expectedEvents, realEv,
		)
	} else {
		for _, eventType := range expectedEvents {
			select {
			case event := <-realEvents:
				if !strings.Contains(event, fmt.Sprintf(" %s ", eventType)) {
					t.Errorf("Test case: %s. Expected %v event, got: %v", testCase, eventType, event)
				}
			default:
				t.Errorf("Test case: %s. Expected %v event, but no event occured", testCase, eventType)
			}
		}
	}
}

// newFakeReconciler returns a new reconcile.Reconciler with a fake client
func newFakeReconciler(initObjects ...runtime.Object) *MachineHealthCheckReconciler {
	return newFakeReconcilerWithCustomRecorder(nil, initObjects...)
}

func newFakeReconcilerWithCustomRecorder(recorder record.EventRecorder, initObjects ...runtime.Object) *MachineHealthCheckReconciler {
	initObjects = append(initObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: MachineNamespace}})

	// we need a rest mapper which knows about the scope of our test CRDs
	rm := meta.NewDefaultRESTMapper([]schema.GroupVersion{
		{
			Group:   InfraRemediationGroup,
			Version: InfraRemediationVersion,
		},
	})
	rm.Add(schema.GroupVersionKind{
		Group:   InfraRemediationGroup,
		Version: InfraRemediationVersion,
		Kind:    InfraRemediationTemplateKind,
	}, meta.RESTScopeNamespace)

	fakeClient := fake.NewClientBuilder().
		WithIndex(&machinev1.Machine{}, utils.MachineNodeNameIndex, indexMachineByNodeName).
		WithRESTMapper(rm).
		WithRuntimeObjects(initObjects...).
		WithStatusSubresource(&machinev1.MachineHealthCheck{}).
		Build()
	caps := cluster.Capabilities{IsOnOpenshift: false, HasMachineAPI: false}
	mhcChecker, _ := mhc.NewMHCChecker(k8sManager, caps, nil)
	return &MachineHealthCheckReconciler{
		Client:                         fakeClient,
		Recorder:                       recorder,
		ClusterUpgradeStatusChecker:    upgradeChecker,
		MHCChecker:                     mhcChecker,
		FeatureGateMHCControllerEvents: make(chan event.GenericEvent),
		FeatureGates: &featuregates.FakeAccessor{
			IsMaoMhcDisabled: true,
		},
		WatchManager: &fakeWatchManager{},
	}
}

func assertBaseReconcile(t *testing.T, tc testCase, ctx context.Context, r *MachineHealthCheckReconciler) {
	recorder := r.Recorder.(*record.FakeRecorder)

	request := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: tc.mhc.GetNamespace(),
			Name:      tc.mhc.GetName(),
		},
	}
	result, err := r.Reconcile(ctx, request)

	assertEvents(t, tc.name, tc.expectedEvents, recorder.Events)
	if tc.expected.error != (err != nil) {
		var errorExpectation string
		if !tc.expected.error {
			errorExpectation = "no"
		}
		t.Errorf("Test case: %s. Expected: %s error, got: %v", tc.name, errorExpectation, err)
	}

	if result != tc.expected.result {
		if tc.expected.result.Requeue {
			before := tc.expected.result.RequeueAfter - time.Second
			after := tc.expected.result.RequeueAfter + time.Second
			if after < result.RequeueAfter || before > result.RequeueAfter {
				t.Errorf("Test case: %s. Expected RequeueAfter between: %v and %v, got: %v", tc.name, before, after, result)
			}
		} else {
			t.Errorf("Test case: %s. Expected: %v, got: %v", tc.name, tc.expected.result, result)
		}
	}
	g := NewWithT(t)
	if tc.expectedStatus != nil {
		mhc := &machinev1.MachineHealthCheck{}
		g.Expect(r.Get(ctx, request.NamespacedName, mhc)).To(Succeed())
		g.Expect(mhc.Status).To(And(
			HaveField("CurrentHealthy", tc.expectedStatus.CurrentHealthy),
			HaveField("ExpectedMachines", tc.expectedStatus.ExpectedMachines),
			HaveField("RemediationsAllowed", tc.expectedStatus.RemediationsAllowed),
		))
		g.Expect(len(mhc.Status.Conditions)).To(Equal(len(tc.expectedStatus.Conditions)))
		for _, expectedCondition := range tc.expectedStatus.Conditions {
			g.Expect(mhc.Status.Conditions).To(ContainElement(
				And(
					HaveField("Type", expectedCondition.Type),
					HaveField("Status", expectedCondition.Status),
				),
			))
		}
	}
}

func assertExternalRemediation(t *testing.T, tc testCase, ctx context.Context, r *MachineHealthCheckReconciler) {
	//When remediationTemplate is set and node transitions to unhealthy, new Remediation Request should be created
	nodeReadyStatus := tc.node.Status.Conditions[0].Status
	if tc.remediationCR == nil {
		//Trying to get External Machine Remediation
		verifyRemediationCR(t, tc, ctx, r, true)
	} else if nodeReadyStatus == corev1.ConditionTrue { //When remediationTemplate is set and node transitions back to healthy, new Remediation Request should be deleted
		//Trying to get External Machine Remediation
		verifyRemediationCR(t, tc, ctx, r, false)
	} else { //When remediationTemplate is already in process
		//Trying to get External Machine Remediation
		verifyRemediationCR(t, tc, ctx, r, true)
	}
}

func buildRunTimeObjects(tc testCase) []runtime.Object {
	var objects []runtime.Object
	objects = append(objects, tc.mhc)
	if tc.machine != nil {
		objects = append(objects, tc.machine)
	}
	objects = append(objects, tc.node)
	if tc.remediationCR != nil {
		objects = append(objects, tc.remediationCR)
	}

	testRemediationKind := InfraRemediationKind
	objects = append(objects, newTestRemediationTemplateCRD(testRemediationKind))
	objects = append(objects, newTestRemediationCRD(testRemediationKind))
	objects = append(objects, newTestRemediationTemplateCR(testRemediationKind, MachineNamespace, InfraRemediationTemplateName))

	templateMultiSupportCR := newTestRemediationTemplateCR(testRemediationKind, MachineNamespace, InfraMultipleSupportRemediationTemplateName)
	templateMultiSupportCR.SetAnnotations(map[string]string{commonannotations.MultipleTemplatesSupportedAnnotation: "true"})
	objects = append(objects, templateMultiSupportCR)

	return objects
}

func verifyRemediationCR(t *testing.T, tc testCase, ctx context.Context, client client.Client, isExist bool) {
	g := NewWithT(t)
	remediationCR := new(unstructured.Unstructured)
	remediationCR.SetAPIVersion(tc.remediationTemplate.GetAPIVersion())
	remediationCR.SetKind(strings.TrimSuffix(tc.remediationTemplate.GetKind(), templateSuffix))
	remediationCR.SetName(tc.machine.GetName())

	nameSpace := types.NamespacedName{
		Namespace: tc.remediationTemplate.GetNamespace(),
		Name:      tc.machine.GetName(),
	}
	if isExist {
		g.Expect(client.Get(ctx, nameSpace, remediationCR)).To(Succeed())
		crAnnotations := remediationCR.GetAnnotations()
		g.Expect(crAnnotations).ToNot(BeNil())
		g.Expect(crAnnotations[commonannotations.NodeNameAnnotation]).Should(Equal(tc.node.Name))
		g.Expect(crAnnotations[annotations.TemplateNameAnnotation]).Should(Equal(tc.remediationTemplate.GetName()))
	} else {
		g.Expect(client.Get(ctx, nameSpace, remediationCR)).NotTo(Succeed())
	}
}

// copied from mao/pkg/util/testing
// fooBar returns foo:bar map that can be used as default label
func fooBar() map[string]string {
	return map[string]string{"foo": "bar"}
}

// newSelector returns new LabelSelector
func newSelector(labels map[string]string) *metav1.LabelSelector {
	return &metav1.LabelSelector{MatchLabels: labels}
}

// newSelectorFooBar returns new foo:bar label selector
func newSelectorFooBar() *metav1.LabelSelector {
	return newSelector(fooBar())
}

// newNode returns new node object that can be used for testing
func newNodeForMHC(name string, ready bool) *corev1.Node {
	nodeReadyStatus := corev1.ConditionTrue
	if !ready {
		nodeReadyStatus = corev1.ConditionUnknown
	}

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceNone,
			Annotations: map[string]string{
				machineAnnotationKey: fmt.Sprintf("%s/%s", MachineNamespace, "fakeMachine"),
			},
			Labels: map[string]string{},
			UID:    uuid.NewUUID(),
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: "v1",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:               corev1.NodeReady,
					Status:             nodeReadyStatus,
					LastTransitionTime: KnownDate,
				},
			},
		},
	}
}

// newMachine returns new machine object that can be used for testing
func newMachine(name string, nodeName string) *machinev1.Machine {
	m := &machinev1.Machine{
		TypeMeta: metav1.TypeMeta{Kind: "Machine"},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: make(map[string]string),
			Name:        name,
			Namespace:   MachineNamespace,
			Labels:      fooBar(),
			UID:         uuid.NewUUID(),
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "MachineSet",
					Controller: pointer.Bool(true),
				},
			},
			// the following line is to account for a change in the fake client, see https://github.com/kubernetes-sigs/controller-runtime/pull/1306
			ResourceVersion: "999",
		},
		Spec: machinev1.MachineSpec{},
	}
	if nodeName != "" {
		m.Status = machinev1.MachineStatus{
			NodeRef: &corev1.ObjectReference{
				Name:      nodeName,
				Namespace: metav1.NamespaceNone,
			},
		}
	}
	return m
}

// newMachineHealthCheck returns new MachineHealthCheck object that can be used for testing
func newMachineHealthCheck(name string, remediationTemplate *corev1.ObjectReference) *machinev1.MachineHealthCheck {
	return &machinev1.MachineHealthCheck{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: MachineNamespace,
			// the following line is to account for a change in the fake client, see https://github.com/kubernetes-sigs/controller-runtime/pull/1306
			ResourceVersion: "999",
			UID:             "1234",
		},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "machine.openshift.io/v1beta1",
			Kind:       "MachineHealthCheck",
		},
		Spec: machinev1.MachineHealthCheckSpec{
			Selector: *newSelectorFooBar(),
			UnhealthyConditions: []machinev1.UnhealthyCondition{
				{
					Type:    "Ready",
					Status:  "Unknown",
					Timeout: metav1.Duration{Duration: 300 * time.Second},
				},
				{
					Type:    "Ready",
					Status:  "False",
					Timeout: metav1.Duration{Duration: 300 * time.Second},
				},
			},
			RemediationTemplate: remediationTemplate,
		},
		Status: machinev1.MachineHealthCheckStatus{},
	}
}

// copied from mao/pkg/util/external
const (
	// TemplateSuffix is the object kind suffix used by infrastructure references associated
	// with MachineSet or MachineDeployments.
	templateSuffix = "Template"
)

// Get uses the client and reference to get an external, unstructured object.
func Get(ctx context.Context, c client.Client, ref *corev1.ObjectReference, namespace string) (*unstructured.Unstructured, error) {
	obj := new(unstructured.Unstructured)
	obj.SetAPIVersion(ref.APIVersion)
	obj.SetKind(ref.Kind)
	obj.SetName(ref.Name)
	key := client.ObjectKey{Name: obj.GetName(), Namespace: namespace}
	if err := c.Get(ctx, key, obj); err != nil {
		return nil, fmt.Errorf("failed to retrieve %s external object %q/%q: %w", obj.GetKind(), key.Namespace, key.Name, err)
	}
	return obj, nil
}

// GenerateTemplate input is everything needed to generate a new template.
type GenerateTemplateInput struct {
	// Template is the TemplateRef turned into an unstructured.
	// +required
	Template *unstructured.Unstructured

	// TemplateRef is a reference to the template that needs to be cloned.
	// +required
	TemplateRef *corev1.ObjectReference

	// Namespace is the Kubernetes MachineNamespace the cloned object should be created into.
	// +required
	Namespace string

	// OwnerRef is an optional OwnerReference to attach to the cloned object.
	// +optional
	OwnerRef *metav1.OwnerReference

	// Labels is an optional map of labels to be added to the object.
	// +optional
	Labels map[string]string
}

func GenerateTemplate(in *GenerateTemplateInput) (*unstructured.Unstructured, error) {
	template, found, err := unstructured.NestedMap(in.Template.Object, "spec", "template")
	if !found {
		return nil, fmt.Errorf("missing Spec.Template on %v %q", in.Template.GroupVersionKind(), in.Template.GetName())
	} else if err != nil {
		return nil, fmt.Errorf("failed to retrieve Spec.Template map on %v %q", in.Template.GroupVersionKind(), in.Template.GetName())
	}

	// Create the unstructured object from the template.
	to := &unstructured.Unstructured{Object: template}
	to.SetResourceVersion("")
	to.SetFinalizers(nil)
	to.SetUID("")
	to.SetSelfLink("")
	to.SetName(names.SimpleNameGenerator.GenerateName(in.Template.GetName() + "-"))
	to.SetNamespace(in.Namespace)

	if to.GetAnnotations() == nil {
		to.SetAnnotations(map[string]string{})
	}
	annotations := to.GetAnnotations()
	annotations[machinev1.TemplateClonedFromNameAnnotation] = in.TemplateRef.Name
	annotations[machinev1.TemplateClonedFromGroupKindAnnotation] = in.TemplateRef.GroupVersionKind().GroupKind().String()
	to.SetAnnotations(annotations)

	// Set the owner reference.
	if in.OwnerRef != nil {
		to.SetOwnerReferences([]metav1.OwnerReference{*in.OwnerRef})
	}

	// Set the object APIVersion.
	if to.GetAPIVersion() == "" {
		to.SetAPIVersion(in.Template.GetAPIVersion())
	}

	// Set the object Kind and strip the word "Template" if it's a suffix.
	if to.GetKind() == "" {
		to.SetKind(strings.TrimSuffix(in.Template.GetKind(), templateSuffix))
	}
	return to, nil
}
