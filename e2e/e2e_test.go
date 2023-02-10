package e2e

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
)

const (
	machineDeletionRemediationNamespace = "openshift-operators"
	machineAnnotationOpenshift          = "machine.openshift.io/machine"
	machineAPIVersion                   = "machine.openshift.io/v1beta1"
	machineKind                         = "Machine"
	workerLabelName                     = "node-role.kubernetes.io/worker"
)

var _ = Describe("E2E tests", func() {
	Context("Machine Deletion Remediation", func() {
		Describe("CR created for an unhealthy node", func() {
			var (
				initialWorkers *v1.NodeList
				node           *v1.Node
				mdr            *v1alpha1.MachineDeletionRemediation
				machine        *unstructured.Unstructured
			)
			BeforeEach(func() {
				// Get the first Worker node available
				req, _ := labels.NewRequirement(workerLabelName, selection.Exists, []string{})
				selector := labels.NewSelector().Add(*req)

				initialWorkers = &v1.NodeList{}
				Expect(k8sClient.List(context.Background(), initialWorkers, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
				Expect(len(initialWorkers.Items)).To(BeNumerically(">=", 2))
				node = &initialWorkers.Items[0]
				machine = getAssociatedMachine(node)
			})

			JustBeforeEach(func() {
				mdr = createRemediation(node)
			})

			AfterEach(func() {
				if mdr != nil {
					deleteRemediation(mdr)
				}
			})

			It("deletes the associated Machine", func() {
				By("checking the Machine was deleted")
				Eventually(func() bool {
					key := client.ObjectKeyFromObject(machine)
					err := k8sClient.Get(context.TODO(), key, createMachineStruct())
					return errors.IsNotFound(err)
				}, 5*time.Minute, 10*time.Second).Should(BeTrue())

				By("checking a new Node was created after the CR")
				req, _ := labels.NewRequirement(workerLabelName, selection.Exists, []string{})
				selector := labels.NewSelector().Add(*req)

				newWorkers := &v1.NodeList{}
				Eventually(func() int {
					Expect(k8sClient.List(context.Background(), newWorkers, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())
					return len(newWorkers.Items)
				}, 15*time.Minute, 10*time.Second).Should(BeNumerically("==", len(initialWorkers.Items)))

				newNode := &v1.Node{}
				for _, n := range newWorkers.Items {
					if n.GetCreationTimestamp().Time.After(mdr.GetCreationTimestamp().Time) {
						newNode = &n
						break
					}
				}
				Expect(newNode).NotTo(BeNil())

				By("checking the new Machine associated with the Node was created after the CR")
				newMachine := getAssociatedMachine(newNode)
				Expect(newMachine.GetUID()).ShouldNot(Equal(machine.GetUID()))
				Expect(newMachine.GetCreationTimestamp().Time).Should(BeTemporally(">=", mdr.GetCreationTimestamp().Time))
			})
		})
	})
})

func createMachineStruct() *unstructured.Unstructured {
	machine := new(unstructured.Unstructured)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(machineAPIVersion)
	return machine

}

func getAssociatedMachine(node *v1.Node) *unstructured.Unstructured {
	parts := strings.Split(node.GetAnnotations()[machineAnnotationOpenshift], "/")
	machineNamespace := parts[0]
	machineName := parts[1]
	key := client.ObjectKey{
		Name:      machineName,
		Namespace: machineNamespace,
	}

	machine := createMachineStruct()
	Expect(k8sClient.Get(context.TODO(), key, machine)).To(Succeed())
	return machine
}

func createRemediation(node *v1.Node) *v1alpha1.MachineDeletionRemediation {
	mdr := &v1alpha1.MachineDeletionRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      node.Name,
			Namespace: machineDeletionRemediationNamespace,
		},
	}

	ExpectWithOffset(1, k8sClient.Create(context.Background(), mdr)).ToNot(HaveOccurred())
	return mdr
}

func deleteRemediation(mdr *v1alpha1.MachineDeletionRemediation) {
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
}
