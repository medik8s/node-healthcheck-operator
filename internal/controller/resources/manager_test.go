package resources

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var _ = Describe("removeConfirmedHealthyAnnotation", func() {
	var (
		fakeClient client.Client
		m          *manager
		nodeName   = "test-node"
	)

	newManager := func(c client.Client) *manager {
		return &manager{
			Client: c,
			ctx:    context.Background(),
			log:    logf.Log.WithName("test"),
		}
	}

	newScheme := func() *runtime.Scheme {
		s := runtime.NewScheme()
		Expect(corev1.AddToScheme(s)).To(Succeed())
		return s
	}

	Context("node has the annotation", func() {
		BeforeEach(func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
					Annotations: map[string]string{
						RemediationManuallyConfirmedHealthyAnnotationKey: "true",
					},
				},
			}
			fakeClient = fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(node).Build()
			m = newManager(fakeClient)
		})

		It("removes the annotation via node Update", func() {
			Expect(m.removeConfirmedHealthyAnnotation(nodeName)).To(Succeed())

			updated := &corev1.Node{}
			Expect(fakeClient.Get(context.Background(), client.ObjectKey{Name: nodeName}, updated)).To(Succeed())
			_, found := updated.GetAnnotations()[RemediationManuallyConfirmedHealthyAnnotationKey]
			Expect(found).To(BeFalse(), "annotation must be removed after node Update")
		})
	})

	Context("node does not have the annotation", func() {
		BeforeEach(func() {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: nodeName},
			}
			fakeClient = fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(node).Build()
			m = newManager(fakeClient)
		})

		It("succeeds without error and does not mutate the node", func() {
			Expect(m.removeConfirmedHealthyAnnotation(nodeName)).To(Succeed())

			unchanged := &corev1.Node{}
			Expect(fakeClient.Get(context.Background(), client.ObjectKey{Name: nodeName}, unchanged)).To(Succeed())
			Expect(unchanged.GetAnnotations()).To(BeEmpty())
		})
	})

	Context("node does not exist", func() {
		BeforeEach(func() {
			fakeClient = fake.NewClientBuilder().WithScheme(newScheme()).Build()
			m = newManager(fakeClient)
		})

		It("returns an error", func() {
			Expect(m.removeConfirmedHealthyAnnotation(nodeName)).To(HaveOccurred())
		})
	})
})
