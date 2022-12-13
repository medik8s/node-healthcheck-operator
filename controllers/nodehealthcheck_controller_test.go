package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/medik8s/node-healthcheck-operator/api/v1alpha1"
	"github.com/medik8s/node-healthcheck-operator/controllers/utils"
)

var _ = Describe("Node Health Check CR", func() {

	Context("Defaults", func() {
		var underTest *v1alpha1.NodeHealthCheck

		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					Selector: metav1.LabelSelector{},
					RemediationTemplate: &v1.ObjectReference{
						Kind:      "InfrastructureRemediationTemplate",
						Namespace: "default",
						Name:      "template",
					},
				},
			}
			err := k8sClient.Create(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			err := k8sClient.Delete(context.Background(), underTest)
			Expect(err).NotTo(HaveOccurred())
		})

		When("creating a resource", func() {
			It("it should have all default values set", func() {
				Expect(underTest.Namespace).To(BeEmpty())
				Expect(underTest.Spec.UnhealthyConditions).To(HaveLen(2))
				Expect(underTest.Spec.UnhealthyConditions[0].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[0].Status).To(Equal(v1.ConditionFalse))
				Expect(underTest.Spec.UnhealthyConditions[0].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))
				Expect(underTest.Spec.UnhealthyConditions[1].Type).To(Equal(v1.NodeReady))
				Expect(underTest.Spec.UnhealthyConditions[1].Status).To(Equal(v1.ConditionUnknown))
				Expect(underTest.Spec.UnhealthyConditions[1].Duration).To(Equal(metav1.Duration{Duration: time.Minute * 5}))
				Expect(underTest.Spec.MinHealthy.StrVal).To(Equal(intstr.FromString("51%").StrVal))
				Expect(underTest.Spec.Selector.MatchLabels).To(BeEmpty())
				Expect(underTest.Spec.Selector.MatchExpressions).To(BeEmpty())
			})
		})

		When("updating status", func() {
			It("succeeds updating only part of the fields", func() {
				Expect(underTest.Status).ToNot(BeNil())
				Expect(underTest.Status.HealthyNodes).To(Equal(0))
				patch := client.MergeFrom(underTest.DeepCopy())
				underTest.Status.HealthyNodes = 1
				underTest.Status.ObservedNodes = 6
				err := k8sClient.Status().Patch(context.Background(), underTest, patch)
				Expect(err).NotTo(HaveOccurred())
				Expect(underTest.Status.HealthyNodes).To(Equal(1))
				Expect(underTest.Status.ObservedNodes).To(Equal(6))
				Expect(underTest.Status.InFlightRemediations).To(BeNil())
			})
		})

	})

	Context("Validation", func() {
		var underTest *v1alpha1.NodeHealthCheck

		BeforeEach(func() {
			underTest = &v1alpha1.NodeHealthCheck{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: v1alpha1.NodeHealthCheckSpec{
					RemediationTemplate: &v1.ObjectReference{
						Kind:      "InfrastructureRemediationTemplate",
						Namespace: "default",
						Name:      "template",
					},
				},
			}
		})

		AfterEach(func() {
			_ = k8sClient.Delete(context.Background(), underTest)
		})

		When("specifying an external remediation template", func() {
			It("should fail creation if empty", func() {
				underTest.Spec.RemediationTemplate = nil
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).To(HaveOccurred())
			})

			It("should succeed creation if a template CR doesn't exists", func() {
				err := k8sClient.Create(context.Background(), underTest)
				Expect(err).NotTo(HaveOccurred())
			})
		})
		When("specifying min healthy", func() {
			It("fails creation on percentage > 100%", func() {
				invalidPercentage := intstr.FromString("150%")
				underTest.Spec.MinHealthy = &invalidPercentage
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeTrue())
			})

			It("fails creation on negative number", func() {
				// This test does not work yet, because the "minimum" validation
				// of kubebuilder does not work for IntOrString.
				// Un-skip this as soon as this is supported.
				// For now negative minHealthy is validated via webhook.
				Skip("Does not work yet")
				invalidInt := intstr.FromInt(-10)
				underTest.Spec.MinHealthy = &invalidInt
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeTrue())
			})

			It("succeeds creation on percentage between 0%-100%", func() {
				validPercentage := intstr.FromString("30%")
				underTest.Spec.MinHealthy = &validPercentage
				err := k8sClient.Create(context.Background(), underTest)
				Expect(errors.IsInvalid(err)).To(BeFalse())
			})
		})
	})

	createObjects := func(objects ...client.Object) {
		for _, obj := range objects {
			Expect(k8sClient.Create(context.Background(), obj)).To(Succeed())
		}
	}

	deleteObjects := func(objects ...client.Object) {
		for _, obj := range objects {
			// ignore errors, CRs might be deleted by reconcile
			_ = k8sClient.Delete(context.Background(), obj)
		}
	}

	Context("Reconciliation", func() {
		var (
			underTest *v1alpha1.NodeHealthCheck
			objects   []client.Object
		)

		setupObjects := func(unhealthy int, healthy int) {
			objects = newNodes(unhealthy, healthy, false)
			underTest = newNodeHealthCheck()
			objects = append(objects, underTest)
		}

		JustBeforeEach(func() {
			createObjects(objects...)
			// give the reconciler some time
			time.Sleep(2 * time.Second)
			// get updated NHC
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
		})

		AfterEach(func() {
			// delete all created objects
			deleteObjects(objects...)

			// delete all remediation CRs
			cr := newRemediationCR("")
			crList := &unstructured.UnstructuredList{Object: cr.Object}
			Expect(k8sClient.List(context.Background(), crList)).To(Succeed())
			for _, item := range crList.Items {
				Expect(k8sClient.Delete(context.Background(), &item)).To(Succeed())
			}

			// let thing settle a bit
			time.Sleep(1 * time.Second)
		})

		When("few nodes are unhealthy and healthy nodes meet min healthy", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
			})

			It("create a remediation CR for each unhealthy node and updates status", func() {
				cr := newRemediationCR("unhealthy-worker-node-1")
				err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(err).NotTo(HaveOccurred())
				Expect(cr.Object).To(ContainElement(map[string]interface{}{"size": "foo"}))
				Expect(cr.GetOwnerReferences()).
					To(ContainElement(
						And(
							// Kind and API version aren't set on underTest, envtest issue...
							// Controller is empty for HaveField because false is the zero value?
							HaveField("Name", underTest.Name),
						),
					))
				Expect(cr.GetAnnotations()[oldRemediationCRAnnotationKey]).To(BeEmpty())

				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
				Expect(underTest.Status.InFlightRemediations).To(HaveLen(1))
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
				Expect(underTest.Status.Reason).ToNot(BeEmpty())
				Expect(underTest.Status.Conditions).To(ContainElement(
					And(
						HaveField("Type", v1alpha1.ConditionTypeDisabled),
						HaveField("Status", metav1.ConditionFalse),
						HaveField("Reason", v1alpha1.ConditionReasonEnabled),
					)))

			})

		})

		When("few nodes are unhealthy and healthy nodes above min healthy", func() {
			BeforeEach(func() {
				setupObjects(4, 3)
			})

			It("skips remediation - CR is not created, status updated correctly", func() {
				cr := newRemediationCR("unhealthy-worker-node-1")
				err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				Expect(underTest.Status.HealthyNodes).To(Equal(3))
				Expect(underTest.Status.ObservedNodes).To(Equal(7))
				Expect(underTest.Status.InFlightRemediations).To(BeEmpty())
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
				Expect(underTest.Status.Reason).ToNot(BeEmpty())
			})

		})

		When("few nodes become healthy", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
				remediationCR := newRemediationCR("healthy-worker-node-2")
				remediationCROther := newRemediationCR("healthy-worker-node-1")
				refs := remediationCROther.GetOwnerReferences()
				refs[0].Name = "other"
				remediationCROther.SetOwnerReferences(refs)
				objects = append(objects, remediationCR, remediationCROther)
			})

			It("deletes an existing remediation CR and updates status", func() {
				cr := newRemediationCR("unhealthy-worker-node-1")
				err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(err).NotTo(HaveOccurred())

				cr = newRemediationCR("healthy-worker-node-2")
				err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				// owned by other NHC, should not be deleted
				cr = newRemediationCR("healthy-worker-node-1")
				err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(err).NotTo(HaveOccurred())

				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
				Expect(underTest.Status.InFlightRemediations).To(HaveLen(1))
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseRemediating))
				Expect(underTest.Status.Reason).ToNot(BeEmpty())
			})
		})

		When("an old remediation cr exist", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
			})

			AfterEach(func() {
				fakeTime = nil
			})

			It("an alert flag is set on remediation cr", func() {
				By("faking time and triggering another reconcile")
				afterTimeout := time.Now().Add(remediationCRAlertTimeout).Add(2 * time.Minute)
				fakeTime = &afterTimeout
				labels := underTest.Labels
				if labels == nil {
					labels = make(map[string]string)
				}
				labels["trigger"] = "now"
				underTest.Labels = labels
				Expect(k8sClient.Update(context.Background(), underTest)).To(Succeed())
				time.Sleep(2 * time.Second)

				cr := newRemediationCR("unhealthy-worker-node-1")
				err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(err).NotTo(HaveOccurred())
				Expect(cr.GetAnnotations()[oldRemediationCRAnnotationKey]).To(Equal("flagon"))
			})
		})

		Context("control plane nodes", func() {
			When("two control plane nodes are unhealthy, just one should be remediated", func() {
				BeforeEach(func() {
					objects = newNodes(2, 1, true)
					objects = append(objects, newNodes(1, 5, false)...)
					underTest = newNodeHealthCheck()
					objects = append(objects, underTest)
				})

				It("creates a one remediation CR for control plane node and updates status", func() {
					cr := newRemediationCR("")
					crList := &unstructured.UnstructuredList{Object: cr.Object}
					Expect(k8sClient.List(context.Background(), crList)).To(Succeed())

					Expect(len(crList.Items)).To(BeNumerically("==", 2), "expected 2 remediations, one for control plane, one for worker")
					Expect(crList.Items).To(ContainElements(
						// the unhealthy worker
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", "unhealthy-worker-node-1"))),
						// one of the unhealthy control plane nodes
						HaveField("Object", HaveKeyWithValue("metadata", HaveKeyWithValue("name", ContainSubstring("unhealthy-control-plane-node")))),
					))
					Expect(underTest.Status.HealthyNodes).To(Equal(6))
					Expect(underTest.Status.ObservedNodes).To(Equal(9))
					Expect(underTest.Status.InFlightRemediations).To(HaveLen(2))
				})
			})
		})

		When("remediation is needed but pauseRequests exists", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
				underTest.Spec.PauseRequests = []string{"I'm an admin, asking you to stop remediating this group of nodes"}
			})

			It("skips remediation and updates status", func() {
				cr := newRemediationCR("unhealthy-worker-node-1")
				err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
				Expect(underTest.Status.InFlightRemediations).To(BeEmpty())
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhasePaused))
				Expect(underTest.Status.Reason).ToNot(BeEmpty())
			})
		})

		When("Nodes are candidates for remediation and cluster is upgrading", func() {
			BeforeEach(func() {
				clusterUpgradeRequeueAfter = 5 * time.Second
				upgradeChecker.Upgrading = true
				setupObjects(1, 2)
			})

			AfterEach(func() {
				upgradeChecker.Upgrading = false
			})

			It("doesn't not remediate but requeues reconciliation and updates status", func() {
				cr := newRemediationCR("unhealthy-worker-node-1")
				err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(errors.IsNotFound(err)).To(BeTrue())

				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
				Expect(underTest.Status.InFlightRemediations).To(HaveLen(0))
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseEnabled))
				Expect(underTest.Status.Reason).ToNot(BeEmpty())

				By("stopping upgrade and waiting for requeue")
				upgradeChecker.Upgrading = false
				time.Sleep(10 * time.Second)
				err = k8sClient.Get(context.Background(), client.ObjectKeyFromObject(cr), cr)
				Expect(err).ToNot(HaveOccurred())

				Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(underTest), underTest)).To(Succeed())
				Expect(underTest.Status.HealthyNodes).To(Equal(2))
				Expect(underTest.Status.ObservedNodes).To(Equal(3))
				Expect(underTest.Status.InFlightRemediations).To(HaveLen(1))
			})

		})

		When("Nodes are candidates for remediation but remediation template is broken", func() {
			BeforeEach(func() {
				setupObjects(1, 2)
				underTest.Spec.RemediationTemplate.Kind = "dummy"
			})

			It("should set corresponding condition", func() {
				Expect(underTest.Status.Phase).To(Equal(v1alpha1.PhaseDisabled))
				Expect(underTest.Status.Reason).To(
					And(
						ContainSubstring("Failed to get"),
						ContainSubstring("dummy"),
					))
				Expect(underTest.Status.Conditions).To(ContainElement(
					And(
						HaveField("Type", v1alpha1.ConditionTypeDisabled),
						HaveField("Status", metav1.ConditionTrue),
						HaveField("Reason", v1alpha1.ConditionReasonDisabledTemplateNotFound),
					)))
			})
		})
	})

	// TODO move to new suite in utils package
	Context("Controller Watches", func() {
		var (
			underTest1 *v1alpha1.NodeHealthCheck
			underTest2 *v1alpha1.NodeHealthCheck
			objects    []client.Object
		)

		JustBeforeEach(func() {
			createObjects(objects...)
			time.Sleep(2 * time.Second)
		})

		AfterEach(func() {
			deleteObjects(objects...)
			time.Sleep(1 * time.Second)
		})

		When("a node changes status and is selectable by one NHC selector", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10, false)
				underTest1 = newNodeHealthCheck()
				underTest2 = newNodeHealthCheck()
				underTest2.Name = "test-2"
				emptySelector, _ := metav1.ParseToLabelSelector("fooLabel=bar")
				underTest2.Spec.Selector = *emptySelector
				objects = append(objects, underTest1, underTest2)
			})

			It("creates a reconcile request", func() {
				handler := utils.NHCByNodeMapperFunc(k8sClient, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-worker-node-1"},
				}
				requests := handler(&updatedNode)
				Expect(len(requests)).To(Equal(1))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest1.GetName()}}))
			})
		})

		When("a node changes status and is selectable by the more 2 NHC selector", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10, false)
				underTest1 = newNodeHealthCheck()
				underTest2 = newNodeHealthCheck()
				underTest2.Name = "test-2"
				objects = append(objects, underTest1, underTest2)
			})

			It("creates 2 reconcile requests", func() {
				handler := utils.NHCByNodeMapperFunc(k8sClient, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-worker-node-1"},
				}
				requests := handler(&updatedNode)
				Expect(len(requests)).To(Equal(2))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest1.GetName()}}))
				Expect(requests).To(ContainElement(reconcile.Request{NamespacedName: types.NamespacedName{Name: underTest2.GetName()}}))
			})
		})
		When("a node changes status and there are no NHC objects", func() {
			BeforeEach(func() {
				objects = newNodes(3, 10, false)
			})

			It("doesn't create reconcile requests", func() {
				handler := utils.NHCByNodeMapperFunc(k8sClient, controllerruntime.Log)
				updatedNode := v1.Node{
					ObjectMeta: controllerruntime.ObjectMeta{Name: "healthy-worker-node-1"},
				}
				requests := handler(&updatedNode)
				Expect(requests).To(BeEmpty())
			})
		})
	})
})

func newRemediationCR(nodeName string) *unstructured.Unstructured {
	return newRemediationCRWithRole(nodeName, false)
}

func newRemediationCRWithRole(nodeName string, isControlPlaneNode bool) *unstructured.Unstructured {
	cr := unstructured.Unstructured{}
	cr.SetName(nodeName)
	cr.SetNamespace("default")
	cr.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   testRemediationCRD.Spec.Group,
		Version: testRemediationCRD.Spec.Versions[0].Name,
		Kind:    testRemediationCRD.Spec.Names.Kind,
	})
	cr.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion: "remediation.medik8s.io/v1alpha1",
			Kind:       "NodeHealthCheck",
			Name:       "test",
			UID:        "1234",
		},
	})
	if isControlPlaneNode {
		cr.SetLabels(map[string]string{
			RemediationControlPlaneLabelKey: "",
		})
	}
	return &cr
}

func newNodeHealthCheck() *v1alpha1.NodeHealthCheck {
	unhealthy := intstr.FromString("51%")
	return &v1alpha1.NodeHealthCheck{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeHealthCheck",
			APIVersion: "remediation.medik8s.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
			UID:  "1234",
		},
		Spec: v1alpha1.NodeHealthCheckSpec{
			Selector:   metav1.LabelSelector{},
			MinHealthy: &unhealthy,
			UnhealthyConditions: []v1alpha1.UnhealthyCondition{
				{
					Type:     v1.NodeReady,
					Status:   v1.ConditionFalse,
					Duration: metav1.Duration{Duration: time.Second * 300},
				},
			},
			RemediationTemplate: &v1.ObjectReference{
				Kind:       "InfrastructureRemediationTemplate",
				APIVersion: "test.medik8s.io/v1alpha1",
				Namespace:  "default",
				Name:       "template",
			},
		},
	}
}

func newNodes(unhealthy int, healthy int, isControlPlane bool) []client.Object {
	o := make([]client.Object, 0, healthy+unhealthy)
	roleName := "-worker"
	if isControlPlane {
		roleName = "-control-plane"
	}
	for i := unhealthy; i > 0; i-- {
		node := newNode(fmt.Sprintf("unhealthy%s-node-%d", roleName, i), v1.NodeReady, v1.ConditionFalse, time.Minute*10, isControlPlane)
		o = append(o, node)
	}
	for i := healthy; i > 0; i-- {
		o = append(o, newNode(fmt.Sprintf("healthy%s-node-%d", roleName, i), v1.NodeReady, v1.ConditionTrue, time.Minute*10, isControlPlane))
	}
	return o
}

func newNode(name string, t v1.NodeConditionType, s v1.ConditionStatus, d time.Duration, isControlPlane bool) client.Object {
	labels := make(map[string]string, 1)
	if isControlPlane {
		labels[utils.ControlPlaneRoleLabel] = ""
	} else {
		labels[utils.WorkerRoleLabel] = ""
	}
	return &v1.Node{
		TypeMeta: metav1.TypeMeta{Kind: "Node"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:               t,
					Status:             s,
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-d)},
				},
			},
		},
	}
}
