package e2e

import (
	"context"
	"fmt"
	"strings"
	"time"

	commonconditions "github.com/medik8s/common/pkg/conditions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

const (
	noMachineDeletionRemediationCRFound   = "noMachineDeletionRemediationCRFound"
	processingConditionNotSetError        = "ProcessingConditionNotSet"
	processingConditionSetButNoMatchError = "ProcessingConditionSetButNoMatch"
	processingConditionSetAndMatchSuccess = "ProcessingConditionSetAndMatch"
	processingConditionStartedInfo        = "{\"processingConditionStatus\": \"True\", \"succededConditionStatus\": \"Unknown\", \"reason\": \"RemediationStarted\"}"
)

var mdr *v1alpha1.MachineDeletionRemediation

var _ = Describe("E2E tests", func() {
	Context("Machine Deletion Remediation", func() {
		Describe("CR created for an unhealthy node", func() {
			var (
				initialWorkers *v1.NodeList
				node           *v1.Node
				machine        *unstructured.Unstructured
				platform       string
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
				Expect(machine).NotTo(BeNil())

				var err error
				platform, err = getPlatform(k8sClient)
				Expect(err).To(BeNil())
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
				By("checking the Status condition Processing is True before machine deletion")
				verifyConditionMatches(commonconditions.ProcessingType, metav1.ConditionTrue)

				By("checking the Machine was deleted")
				Eventually(func() bool {
					key := client.ObjectKeyFromObject(machine)
					err := k8sClient.Get(context.TODO(), key, createMachineStruct())
					return errors.IsNotFound(err)
				}, "5m", "10s").Should(BeTrue())

				By("checking the Status condition Processing is still True after machine deletion")
				verifyConditionMatches(commonconditions.ProcessingType, metav1.ConditionTrue)

				msg := fmt.Sprintf("checking the Status condition PermanentNodeDeletionExpected to be False on platform BareMetal, True otherwise (this platform is %s)", platform)
				By(msg)
				if platform == "BareMetal" {
					verifyConditionMatches(commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionFalse)
				} else {
					verifyConditionMatches(commonconditions.PermanentNodeDeletionExpectedType, metav1.ConditionTrue)
				}

				By("checking a new Node and the Machine associated were created after the CR")
				Eventually(func(g Gomega) {
					newWorkers := &v1.NodeList{}
					req, _ := labels.NewRequirement(workerLabelName, selection.Exists, []string{})
					selector := labels.NewSelector().Add(*req)

					g.Expect(k8sClient.List(context.Background(), newWorkers, &client.ListOptions{LabelSelector: selector})).ToNot(HaveOccurred())

					var newNode *v1.Node
					for _, n := range newWorkers.Items {
						if n.GetCreationTimestamp().Time.After(mdr.GetCreationTimestamp().Time) {
							newNode = &n
							break
						}
					}
					g.Expect(newNode).NotTo(BeNil())

					newMachine := getAssociatedMachine(newNode)
					g.Expect(newMachine).NotTo(BeNil())
					g.Expect(newMachine.GetUID()).ShouldNot(Equal(machine.GetUID()))
					g.Expect(newMachine.GetCreationTimestamp().Time).Should(BeTemporally(">=", mdr.GetCreationTimestamp().Time))
				}, "15m", "10s").Should(Succeed())

				By("checking the Status condition Processing is False after node re-provision")
				verifyConditionMatches(commonconditions.ProcessingType, metav1.ConditionFalse)
				By("checking the Status condition Succeeded is True after node re-provision")
				verifyConditionMatches(commonconditions.SucceededType, metav1.ConditionTrue)
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
	if len(parts) != 2 {
		return nil
	}
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

func verifyConditionMatches(conditionType string, conditionStatus metav1.ConditionStatus) {

	Eventually(func() string {
		if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(mdr), mdr); err != nil {
			return noMachineDeletionRemediationCRFound
		}
		gotCondition := meta.FindStatusCondition(mdr.Status.Conditions, conditionType)
		if gotCondition == nil {
			return processingConditionNotSetError
		}
		if meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, conditionType, conditionStatus) {
			return processingConditionSetAndMatchSuccess
		}
		return processingConditionSetButNoMatchError
	}, "60s", "1s").Should(Equal(processingConditionSetAndMatchSuccess))
}

func getPlatform(c client.Client) (string, error) {

	cluster := &unstructured.Unstructured{}
	cluster.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "config.openshift.io",
		Version: "v1",
		Kind:    "Infrastructure",
	})
	key := client.ObjectKey{
		Name:      "cluster",
		Namespace: "",
	}
	err := c.Get(context.Background(), key, cluster)
	if err != nil {
		return "", err
	}
	return getClusterPlatform(cluster), nil
}

func getClusterPlatform(u *unstructured.Unstructured) string {
	valueName := "status"
	valMap, exists, err := unstructured.NestedMap(u.Object, valueName)
	if err != nil {
		log.Error(err, "could not get value", "value", valueName)
		return ""
	}
	if !exists {
		log.Info("object does not have value", "value", valueName)
		return ""
	}

	valueName = "platformStatus"
	valMap, exists, err = unstructured.NestedMap(valMap, valueName)
	if err != nil {
		log.Error(err, "could not get value", "value", valueName)
		return ""
	}
	if !exists {
		log.Info("object does not have value", "value", valueName)
		return ""
	}

	valueName = "type"
	value, exists, err := unstructured.NestedString(valMap, valueName)
	if err != nil {
		log.Error(err, "could not get value", "value", valueName)
		return ""
	}
	if !exists {
		log.Info("object does not have value", "value", valueName)
		return ""
	}
	return value
}
