package utils

import (
	"fmt"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

const (
	testContainerName  = "test"
	testContainerImage = "test:latest"
	testAppLabel       = "app"
)

var (
	deploymentUID  = types.UID("deployment-uid-12345")
	replicaSetUID  = types.UID("replicaset-uid-12345")
	statefulSetUID = types.UID("statefulset-uid-12345")
	customUID      = types.UID("custom-uid-12345")
)

var _ = Describe("GetPodName", func() {
	const podNameEnvVar = "POD_NAME"
	var originalValue string
	var wasSet bool

	BeforeEach(func() {
		originalValue, wasSet = os.LookupEnv(podNameEnvVar)
	})

	AfterEach(func() {
		if wasSet {
			os.Setenv(podNameEnvVar, originalValue)
		} else {
			os.Unsetenv(podNameEnvVar)
		}
	})

	It("should return pod name when POD_NAME is set", func() {
		expectedPodName := "test-pod-abc123"
		os.Setenv(podNameEnvVar, expectedPodName)

		podName, err := GetPodName()
		Expect(err).ToNot(HaveOccurred())
		Expect(podName).To(Equal(expectedPodName))
	})

	It("should return error when POD_NAME is not set", func() {
		os.Unsetenv(podNameEnvVar)

		_, err := GetPodName()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("POD_NAME must be set"))
	})

	It("should return error when POD_NAME is empty", func() {
		os.Setenv(podNameEnvVar, "")

		_, err := GetPodName()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("POD_NAME is empty"))
	})
})

var _ = Describe("GetOwningDeployment", func() {
	var namespace string
	var testIndex int

	BeforeEach(func() {
		testIndex++
		namespace = fmt.Sprintf("test-ns-%d", testIndex)
		// Create namespace for this test
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	})

	AfterEach(func() {
		// Delete namespace; cascade deletion removes all resources within it (Pods, ReplicaSets, Deployments, etc.)
		// Each test uses a unique namespace name, so no conflict if deletion is still in progress for the next test
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Delete(ctx, ns)).To(Succeed())
	})

	It("should successfully walk ownership chain: Pod → ReplicaSet → Deployment", func() {
		deployment := createDeployment(namespace, "test-deployment")
		replicaSet := createReplicaSetOwnedByDeployment(namespace, "test-deployment-abc123", deployment)
		pod := createPodOwnedByReplicaSet(namespace, "test-deployment-abc123-xyz456", replicaSet)

		result, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).ToNot(BeNil())
		Expect(result.Name).To(Equal(deployment.Name))
		Expect(result.Namespace).To(Equal(namespace))
		Expect(result.UID).To(Equal(deployment.UID))
	})

	It("should return error when pod does not exist", func() {
		_, err := GetOwningDeployment(ctx, k8sClient, namespace, "non-existent-pod")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get pod"))
	})

	It("should return error when pod has no owner reference", func() {
		pod := createOrphanPod(namespace)

		_, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("pod"))
		Expect(err.Error()).To(ContainSubstring("has no controller owner reference"))
	})

	It("should return error when pod is owned by non-ReplicaSet", func() {
		pod := createPodOwnedByStatefulSet(namespace)

		_, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected ReplicaSet"))
	})

	It("should return error when ReplicaSet does not exist", func() {
		pod := createPodWithMissingReplicaSet(namespace)

		_, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get replicaset"))
	})

	It("should return error when ReplicaSet has no owner reference", func() {
		replicaSet := createOrphanReplicaSet(namespace)
		pod := createPodOwnedByReplicaSet(namespace, "test-pod", replicaSet)

		_, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("replicaset"))
		Expect(err.Error()).To(ContainSubstring("has no controller owner reference"))
	})

	It("should return error when ReplicaSet is owned by non-Deployment", func() {
		replicaSet := createReplicaSetOwnedByCustomController(namespace)
		pod := createPodOwnedByReplicaSet(namespace, "test-pod", replicaSet)

		_, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("expected Deployment"))
	})

	It("should return error when Deployment does not exist", func() {
		replicaSet := createReplicaSetOwnedByMissingDeployment(namespace)
		pod := createPodOwnedByReplicaSet(namespace, "test-pod", replicaSet)

		_, err := GetOwningDeployment(ctx, k8sClient, namespace, pod.Name)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get deployment"))
	})
})

// Helper functions for creating test objects

func createDeployment(namespace, name string) *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       deploymentUID,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{testAppLabel: name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{testAppLabel: name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  testContainerName,
							Image: testContainerImage,
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, deployment)).To(Succeed())
	return deployment
}

func createReplicaSetOwnedByDeployment(namespace, name string, deployment *appsv1.Deployment) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       replicaSetUID,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       deployment.Name,
					UID:        deployment.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{testAppLabel: deployment.Name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{testAppLabel: deployment.Name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  testContainerName,
							Image: testContainerImage,
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, rs)).To(Succeed())
	return rs
}

func createReplicaSetOwnedByMissingDeployment(namespace string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rs",
			Namespace: namespace,
			UID:       replicaSetUID,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "missing-deployment",
					UID:        deploymentUID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{testAppLabel: "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{testAppLabel: "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  testContainerName,
							Image: testContainerImage,
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, rs)).To(Succeed())
	return rs
}

func createOrphanReplicaSet(namespace string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-replicaset",
			Namespace: namespace,
			UID:       replicaSetUID,
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{testAppLabel: "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{testAppLabel: "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  testContainerName,
							Image: testContainerImage,
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, rs)).To(Succeed())
	return rs
}

func createReplicaSetOwnedByCustomController(namespace string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-rs",
			Namespace: namespace,
			UID:       replicaSetUID,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "custom.io/v1",
					Kind:       "CustomController",
					Name:       "custom-controller",
					UID:        customUID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{testAppLabel: "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{testAppLabel: "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  testContainerName,
							Image: testContainerImage,
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, rs)).To(Succeed())
	return rs
}

func createPodOwnedByReplicaSet(namespace, name string, replicaSet *appsv1.ReplicaSet) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "ReplicaSet",
					Name:       replicaSet.Name,
					UID:        replicaSet.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  testContainerName,
					Image: testContainerImage,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

func createOrphanPod(namespace string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orphan-pod",
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  testContainerName,
					Image: testContainerImage,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

func createPodOwnedByStatefulSet(namespace string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "statefulset-pod",
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "StatefulSet",
					Name:       "test-statefulset",
					UID:        statefulSetUID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  testContainerName,
					Image: testContainerImage,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}

func createPodWithMissingReplicaSet(namespace string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "ReplicaSet",
					Name:       "missing-replicaset",
					UID:        replicaSetUID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  testContainerName,
					Image: testContainerImage,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return pod
}
