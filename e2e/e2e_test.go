package e2e

import (
	"context"
	"strings"
	"time"

	"github.com/medik8s/machine-deletion/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	machineDeletionNamespace   = "openshift-operators"
	machineAnnotationOpenshift = "machine.openshift.io/machine"
	machineAPIVersion          = "machine.openshift.io/v1beta1"
	machineKind                = "Machine"
	workerLabelName            = "node-role.kubernetes.io/worker"
)

var _ = Describe("E2E tests", func() {
	Context("Machine Deletion Remediation", func() {
		var (
			node             *v1.Node
			mdr              *v1alpha1.MachineDeletion
			machineNamespace string
			machineName      string
		)
		BeforeEach(func() {
			// Get the first Worker node available
			req, _ := labels.NewRequirement(workerLabelName, selection.Exists, []string{})

			selector := labels.NewSelector().Add(*req)

			workers := &v1.NodeList{}
			Expect(k8sClient.List(context.Background(), workers, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
			Expect(len(workers.Items)).To(BeNumerically(">=", 2))
			node = &workers.Items[0]

			//Get the Machine associated with the Node
			machineNamespace = strings.Split(node.GetAnnotations()[machineAnnotationOpenshift], "/")[0]
			machineName = strings.Split(node.GetAnnotations()[machineAnnotationOpenshift], "/")[1]
			key := client.ObjectKey{
				Name:      machineName,
				Namespace: machineNamespace,
			}

			Expect(k8sClient.Get(context.TODO(), key, getMachine())).To(Succeed())
		})

		AfterEach(func() {
			if mdr != nil {
				deleteRemediation(mdr)
			}
		})

		Describe("CR created for an unhealthy node", func() {
			It("deletes the associated Machine", func() {
				mdr = createRemediation(node)
				verifyMachineIsDeleted(machineName, machineNamespace)
			})
		})
	})
})

func getMachine() *unstructured.Unstructured {
	machine := new(unstructured.Unstructured)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(machineAPIVersion)
	return machine

}

func createRemediation(node *v1.Node) *v1alpha1.MachineDeletion {
	mdr := &v1alpha1.MachineDeletion{}
	mdr.Name = node.Name
	mdr.Namespace = machineDeletionNamespace

	ExpectWithOffset(1, k8sClient.Create(context.Background(), mdr)).ToNot(HaveOccurred())
	return mdr
}

func deleteRemediation(mdr *v1alpha1.MachineDeletion) {
	timeout := 2 * time.Minute
	pollInterval := 10 * time.Second
	// Delete
	EventuallyWithOffset(1, func() error {
		err := k8sClient.Delete(context.Background(), mdr)
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}, timeout, pollInterval).ShouldNot(HaveOccurred(), "failed to delete mdr")

	// Wait until deleted
	EventuallyWithOffset(1, func() bool {
		err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(mdr), mdr)
		if errors.IsNotFound(err) {
			return true
		}
		return false
	}, timeout, pollInterval).Should(BeTrue(), "mdr not deleted in time")
}

func verifyMachineIsDeleted(machineName, machineNamespace string) {
	timeout := 5 * time.Minute
	pollInterval := 10 * time.Second

	key := client.ObjectKey{
		Name:      machineName,
		Namespace: machineNamespace,
	}

	EventuallyWithOffset(1, func() bool {
		err := k8sClient.Get(context.TODO(), key, getMachine())
		return errors.IsNotFound(err)
	}, timeout, pollInterval).Should(BeTrue())
}
