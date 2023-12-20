/*
Copyright 2021.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	commonannotations "github.com/medik8s/common/pkg/annotations"
	commonconditions "github.com/medik8s/common/pkg/conditions"
	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	machinev1 "github.com/openshift/api/machine/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
)

const (
	machineAnnotationOpenshift = "machine.openshift.io/machine"
	// MachineNameNsAnnotation contains to-be-deleted Machine's Name and Namespace
	MachineNameNsAnnotation = "machine-deletion-remediation.medik8s.io/machineNameNamespace"
	// MachineOwnerAnnotation contains Machine's ownerReference name and Kind
	MachineOwnerAnnotation = "machine-deletion-remediation.medik8s.io/machineOwner"
	// Infos
	postponedMachineDeletionInfo  = "node-associated machine was not deleted yet"
	successfulMachineDeletionInfo = "node-associated machine correctly deleted"
	//Errors
	noAnnotationsError                 = "failed to find machine annotation on node name: %s"
	noMachineAnnotationError           = "failed to find openshift machine annotation on node name: %s"
	invalidValueMachineAnnotationError = "failed to extract Machine Name and Machine Namespace from machine annotation on the node for node name: %s"
	failedToDeleteMachineError         = "failed to delete machine of node name: %s"
	nodeNotFoundErrorMsg               = "failed to fetch node"
	machineNotFoundErrorMsg            = "failed to fetch machine of node"
	// Cluster Provider messages
	machineDeletedOnCloudProviderMessage     = "Machine will be deleted and the unhealthy node replaced. This is a Cloud cluster provider: the new node is expected to have a new name"
	machineDeletedOnBareMetalProviderMessage = "Machine will be deleted and the unhealthy node replaced. This is a BareMetal cluster provider: the new node is NOT expected to have a new name"
	machineDeletedOnUnknownProviderMessage   = "Machine will be deleted and the unhealthy node replaced. Unknown cluster provider: no information about the new node's name"
)

type conditionChangeReason string

const (
	remediationStarted                  conditionChangeReason = "RemediationStarted"
	remediationTimedOutByNhc            conditionChangeReason = "RemediationStoppedByNHC"
	remediationFinishedMachineDeleted   conditionChangeReason = "MachineDeleted"
	remediationSkippedNodeNotFound      conditionChangeReason = "RemediationSkippedNodeNotFound"
	remediationSkippedMachineNotFound   conditionChangeReason = "RemediationSkippedMachineNotFound"
	remediationSkippedNoControllerOwner conditionChangeReason = "RemediationSkippedNoControllerOwner"
	remediationFailed                   conditionChangeReason = "remediationFailed"
)

var (
	nodeNotFoundError    = errors.New(nodeNotFoundErrorMsg)
	machineNotFoundError = errors.New(machineNotFoundErrorMsg)
	unrecoverableError   = errors.New("unrecoverable error")
)

// MachineDeletionRemediationReconciler reconciles a MachineDeletionRemediation object
type MachineDeletionRemediationReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=machine-deletion-remediation.medik8s.io,resources=machinedeletionremediations,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=machine-deletion-remediation.medik8s.io,resources=machinedeletionremediations/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=machine-deletion-remediation.medik8s.io,resources=machinedeletionremediations/finalizers,verbs=update
//+kubebuilder:rbac:groups=machine.openshift.io,resources=machines,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=machine.openshift.io,resources=machinesets,verbs=get;list;watch
//+kubebuilder:rbac:groups=machine.openshift.io,resources=controlplanemachinesets,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=nodes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the MachineDeletionRemediationRemediation object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *MachineDeletionRemediationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (finalResult ctrl.Result, finalErr error) {
	log := r.Log.WithValues("machinedeletionremediation", req.NamespacedName)

	log.Info("reconciling...")

	var err error
	var mdr *v1alpha1.MachineDeletionRemediation
	if mdr, err = r.getRemediation(ctx, req); mdr == nil || err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Machine Deletion Remediation CR found", "name", mdr.GetName())

	defer func() {
		if updateErr := r.updateStatus(ctx, mdr); updateErr != nil {
			if !apiErrors.IsConflict(updateErr) {
				finalErr = utilerrors.NewAggregate([]error{updateErr, finalErr})
			}
			finalResult.RequeueAfter = time.Second
		}
	}()

	if r.isTimedOutByNHC(mdr) {
		log.Info("NHC time out annotation found, stopping remediation")
		_, err = r.updateConditions(remediationTimedOutByNhc, mdr)
		return ctrl.Result{}, err
	}

	if updateRequired, err := r.updateConditions(remediationStarted, mdr); err != nil {
		log.Error(err, "could not update Status conditions")
		return ctrl.Result{}, err
	} else if updateRequired {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	machine, err := r.getMachine(ctx, mdr)
	if err != nil {
		// Handling specific error scenarios. We avoid re-queue by returning nil after updating the
		// conditions, as these are unrecoverable errors and re-queue would not change the
		// situation. An error is returned only if it does not match the following custom errors, or
		// updateConditions fails.
		if err == nodeNotFoundError {
			_, err = r.updateConditions(remediationSkippedNodeNotFound, mdr)
		} else if err == machineNotFoundError {
			_, err = r.updateConditions(remediationSkippedMachineNotFound, mdr)
		} else if err == unrecoverableError {
			_, err = r.updateConditions(remediationFailed, mdr)
		}

		return ctrl.Result{}, err
	}

	// machine is nil and no errors: we assume that the Machine was deleted successfully upon our request.
	// Otherwise, there are two possibilities:
	// 1. The machine is still to be deleted or pending deletion.
	// 2. The machine was re-provisioned and it is newer than the remediation CR.
	// If the latter, wait for the expected number of nodes to be restored before setting the Succeeded condition.
	// NOTE: the Machine will always be nil after deletion if it changes name after re-provisioning, this is why we
	// verify nodes count restoration even if machine == nil.
	if machine == nil || machine.GetCreationTimestamp().After(mdr.GetCreationTimestamp().Time) {
		if isRestored, err := r.isExpectedNodesNumberRestored(ctx, mdr); err != nil {
			log.Error(err, "could not verify if node was restored")
			if err == unrecoverableError {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		} else if isRestored {
			_, err = r.updateConditions(remediationFinishedMachineDeleted, mdr)
			return ctrl.Result{}, err
		}
		log.Info("waiting for the nodes count to be re-provisioned")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	log.Info("target machine found", "machine", machine.GetName())

	// Detect if Node name is expected to change after Machine deletion given
	// the Platform type. In case of BareMetal platform, the name is NOT
	// expected to change, for other providers, instead, the name is expected
	// to change.
	var status metav1.ConditionStatus
	var permanentNodeDeletionExpectedMsg string

	// The providerID contains the platform type as prefix (e.g. baremetal:///...)
	if machine.Spec.ProviderID == nil || *machine.Spec.ProviderID == "" {
		log.Info("Machine does not have ProviderID")
		permanentNodeDeletionExpectedMsg = machineDeletedOnUnknownProviderMessage
		status = metav1.ConditionUnknown
	} else if strings.HasPrefix(*machine.Spec.ProviderID, "baremetal") {
		permanentNodeDeletionExpectedMsg = machineDeletedOnBareMetalProviderMessage
		status = metav1.ConditionFalse
	} else {
		permanentNodeDeletionExpectedMsg = machineDeletedOnCloudProviderMessage
		status = metav1.ConditionTrue
	}

	if updateRequired := r.setPermanentNodeDeletionExpectedCondition(status, mdr); updateRequired {
		log.Info(permanentNodeDeletionExpectedMsg)
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if !machine.GetDeletionTimestamp().IsZero() {
		// Machine deletion requested already. Log deletion progress until the Machine exists
		log.Info(postponedMachineDeletionInfo, "machine", machine.Name, "machine status.phase", machine.Status.Phase)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if !hasControllerOwner(machine) {
		log.Info("ignoring remediation of the machine: the machine has no controller owner", "machine", machine.GetName())
		_, err = r.updateConditions(remediationSkippedNoControllerOwner, mdr)
		return ctrl.Result{}, err
	}

	// save Machine's name and namespace to follow its deletion phase
	if err = r.saveMachineData(ctx, mdr, machine); err != nil {
		log.Error(err, "could not save Machine's Name and Namespace", "machine name", machine.GetName(), "machine namespace", machine.GetNamespace())
		return ctrl.Result{}, errors.Wrapf(err, "failed to save Machine's name and namespace")
	}

	log.Info("request machine deletion", "machine", machine.GetName())
	err = r.Delete(ctx, machine)
	if err != nil {
		log.Error(err, "failed to delete machine", "machine", machine.GetName())
		return ctrl.Result{}, err
	}

	// requeue immediately to check machine deletion progression
	return ctrl.Result{Requeue: true}, nil
}

func hasControllerOwner(machine *machinev1beta1.Machine) bool {
	refs := machine.GetOwnerReferences()
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineDeletionRemediationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MachineDeletionRemediation{}).
		Complete(r)
}

func (r *MachineDeletionRemediationReconciler) getRemediation(ctx context.Context, req ctrl.Request) (*v1alpha1.MachineDeletionRemediation, error) {
	remediation := &v1alpha1.MachineDeletionRemediation{}
	key := client.ObjectKey{Name: req.Name, Namespace: req.Namespace}
	if err := r.Client.Get(ctx, key, remediation); err != nil {
		if apiErrors.IsNotFound(err) {
			r.Log.Info("MDR already deleted, nothing to do")
			return nil, nil
		}
		r.Log.Error(err, "could not find remediation object in namespace", "remediation name", req.Name, "namespace", req.Namespace)
		return nil, err
	}
	return remediation, nil
}

func (r *MachineDeletionRemediationReconciler) getNodeFromCR(ctx context.Context, mdr *v1alpha1.MachineDeletionRemediation) (*v1.Node, error) {
	node := &v1.Node{}
	key := client.ObjectKey{
		Name: mdr.Name,
	}

	if err := r.Get(ctx, key, node); err != nil {
		return nil, err
	}
	return node, nil
}

// getMachine retrieves a Machine from the cluster based on the remediation.
// It returns the machine and an error if any occurred during the retrieval process.
func (r *MachineDeletionRemediationReconciler) getMachine(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation) (*machinev1beta1.Machine, error) {
	// The Name and Namespace to retrieve the target Machine can come from the following sources:
	// - the remediation's ownerReference: if the remediation was created by MachineHealthcheck
	// - the remediation's Node: if the remediation was created by NodeHealthcheck or manually
	// - the remediation's MachineNameNsAnnotation annotation: once the Machine is found and its Name and Namespace are saved in

	// Try to get first the Machine's data from the remediation's annotation, if any. It means that the Machine was already
	// been found in a previous cycle and we can use the data to verify if the Machine was deleted upon our request or not
	machineName, machineNs, err := getRemediationDataFromAnnotation(remediation, MachineNameNsAnnotation)
	if err != nil {
		r.Log.Error(err, "could not get Machine data from remediation", "remediation", remediation.GetName(), "annotation", MachineNameNsAnnotation)
		return nil, unrecoverableError
	}

	// If the Machine's Name is not in the annotation, it means that it must come from the other two sources and in
	// turns it means that the Machine must exist in the cluster, otherwise an error is returned.
	mustExist := machineName == ""

	if machineName == "" && len(remediation.GetOwnerReferences()) > 0 {
		machineName, machineNs = r.getMachineNameNsFromOwnerReference(ctx, remediation)
		if machineName != "" {
			r.Log.Info("MHC created CR: target machine is CR ownerReference", "machine", machineName, "namespace", machineNs)
		}
	}

	if machineName == "" {
		r.Log.Info("trying to get target Machine from the Node", "node", remediation.Name)
		machineName, machineNs, err = r.getMachineNameNsFromRemediationName(ctx, remediation)
		if err != nil {
			if apiErrors.IsNotFound(err) {
				r.Log.Error(err, nodeNotFoundErrorMsg, "node name", remediation.Name)
				err = nodeNotFoundError
			}
			return nil, err
		}
	}

	machine := &machinev1beta1.Machine{}
	key := client.ObjectKey{
		Name:      machineName,
		Namespace: machineNs,
	}

	r.Log.Info("Looking for the target Machine", "machine", machineName, "namespace", machineNs)
	if err := r.Get(ctx, key, machine); err != nil {
		if !apiErrors.IsNotFound(err) {
			return nil, err
		}

		// Machine was not found
		if mustExist {
			r.Log.Error(err, machineNotFoundErrorMsg, "node", remediation.Name, "machine", machineName)
			return nil, machineNotFoundError
		}

		r.Log.Info(successfulMachineDeletionInfo, "node", remediation.Name, "machine", machineName)
		return nil, nil
	}

	return machine, nil
}

// saveMachineData saves target Machine data in a remediation's annotation
func (r *MachineDeletionRemediationReconciler) saveMachineData(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation, machine *machinev1beta1.Machine) error {
	annotations := remediation.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}

	if _, exists := annotations[MachineNameNsAnnotation]; !exists {
		annotations[MachineNameNsAnnotation] =
			fmt.Sprintf("%s/%s", machine.Namespace, machine.Name)
	}

	if _, exists := annotations[MachineOwnerAnnotation]; !exists {
		name, kind, err := getMachineOwnerNameKind(machine)
		if err != nil {
			return err
		}
		annotations[MachineOwnerAnnotation] = fmt.Sprintf("%s/%s", kind, name)
	}

	remediation.SetAnnotations(annotations)

	return r.Update(ctx, remediation)
}

// getRemediationDataFromAnnotation returns the data saved in the provided annotation of the remediation.
func getRemediationDataFromAnnotation(remediation *v1alpha1.MachineDeletionRemediation, annotation string) (string, string, error) {
	annotations := remediation.GetAnnotations()
	data, exists := annotations[annotation]
	if !exists {
		return "", "", nil
	}

	if dataSlice := strings.Split(data, "/"); len(dataSlice) == 2 {
		return dataSlice[1], dataSlice[0], nil
	}

	msg := "could not get annotation data '%s':%s': error %s"
	return "", "", fmt.Errorf(msg, annotation, data, invalidValueMachineAnnotationError)
}

func (r *MachineDeletionRemediationReconciler) updateStatus(ctx context.Context, mdr *v1alpha1.MachineDeletionRemediation) error {
	if err := r.Client.Status().Update(ctx, mdr); err != nil {
		if !apiErrors.IsConflict(err) {
			r.Log.Error(err, "failed to update mdr status")
		}
		return err
	}
	return nil
}

// updateConditions updates the status conditions of a MachineDeletionRemediation object based on the provided conditionChangeReason.
// note that it does not update server copy of MachineDeletionRemediation object
// return a boolean, indicating if the Status Condition needed to be updated or not, and an error if an unknown conditionChangeReason is provided
func (r *MachineDeletionRemediationReconciler) updateConditions(reason conditionChangeReason, mdr *v1alpha1.MachineDeletionRemediation) (bool, error) {
	var processingConditionStatus, succeededConditionStatus metav1.ConditionStatus

	switch reason {
	case remediationStarted:
		processingConditionStatus = metav1.ConditionTrue
		succeededConditionStatus = metav1.ConditionUnknown
	case remediationFinishedMachineDeleted:
		processingConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionTrue
	case remediationTimedOutByNhc,
		remediationSkippedNoControllerOwner,
		remediationSkippedNodeNotFound,
		remediationSkippedMachineNotFound,
		remediationFailed:
		processingConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionFalse
	default:
		err := fmt.Errorf("unknown conditionChangeReason:%s", reason)
		r.Log.Error(err, "couldn't update MDR Status Conditions")
		return false, err
	}

	// if ProcessingType is already false, it cannot be changed to true again
	if processingConditionStatus == metav1.ConditionTrue &&
		meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, commonconditions.ProcessingType, metav1.ConditionFalse) {
		return false, nil
	}

	// if the requested Status.Conditions are already set, skip update
	if meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, commonconditions.ProcessingType, processingConditionStatus) &&
		meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, commonconditions.SucceededType, succeededConditionStatus) {
		return false, nil
	}

	r.Log.Info("updating Status Condition", "processingConditionStatus", processingConditionStatus, "succededConditionStatus", succeededConditionStatus, "reason", string(reason))
	meta.SetStatusCondition(&mdr.Status.Conditions, metav1.Condition{
		Type:   commonconditions.ProcessingType,
		Status: processingConditionStatus,
		Reason: string(reason),
	})

	meta.SetStatusCondition(&mdr.Status.Conditions, metav1.Condition{
		Type:   commonconditions.SucceededType,
		Status: succeededConditionStatus,
		Reason: string(reason),
	})

	return true, nil
}

func (r *MachineDeletionRemediationReconciler) setPermanentNodeDeletionExpectedCondition(status metav1.ConditionStatus, mdr *v1alpha1.MachineDeletionRemediation) bool {
	var reason, message string
	switch status {
	case metav1.ConditionTrue:
		reason = v1alpha1.MachineDeletionOnCloudProviderReason
		message = machineDeletedOnCloudProviderMessage
	case metav1.ConditionFalse:
		reason = v1alpha1.MachineDeletionOnBareMetalProviderReason
		message = machineDeletedOnBareMetalProviderMessage
	case metav1.ConditionUnknown:
		reason = v1alpha1.MachineDeletionOnUndefinedProviderReason
		message = machineDeletedOnUnknownProviderMessage
	}

	if meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, commonconditions.PermanentNodeDeletionExpectedType, status) {
		return false
	}

	r.Log.Info("updating Status Condition", "PermanentNodeDeletionExpected", status, "reason", reason, "message", message)
	meta.SetStatusCondition(&mdr.Status.Conditions, metav1.Condition{
		Type:    commonconditions.PermanentNodeDeletionExpectedType,
		Status:  status,
		Reason:  reason,
		Message: message,
	})

	return true
}

// isTimedOutByNHC checks if NHC set a timeout annotation on the CR
func (r *MachineDeletionRemediationReconciler) isTimedOutByNHC(remediation *v1alpha1.MachineDeletionRemediation) bool {
	if remediation != nil && remediation.Annotations != nil && remediation.DeletionTimestamp == nil {
		_, isTimeoutIssued := remediation.Annotations[commonannotations.NhcTimedOut]
		return isTimeoutIssued
	}
	return false
}

// isExpectedNodesNumberRestored checks if the number of nodes associated to the Machine Owner is equal to the
// Machine Owner's Replicas value
func (r *MachineDeletionRemediationReconciler) isExpectedNodesNumberRestored(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation) (bool, error) {
	_, namespace, err := getRemediationDataFromAnnotation(remediation, MachineNameNsAnnotation)
	if err != nil {
		return false, errors.Wrap(unrecoverableError, err.Error())
	}

	name, kind, err := getRemediationDataFromAnnotation(remediation, MachineOwnerAnnotation)
	if err != nil {
		return false, errors.Wrap(unrecoverableError, err.Error())
	}

	replicas, err := r.getMachineOwnerSpecReplicas(ctx, kind, name, namespace)
	if err != nil {
		r.Log.Error(err, "could not get Machine owner's Spec.Replicas", "kind", kind, "name", name, "namespace", namespace)
		return false, err
	}

	if replicas == 0 {
		r.Log.Info("Machine owner's Spec.Replicas is 0, no need to verify Node count restoration")
		return true, nil
	}

	nodes, err := r.getMachineOwnerNodes(ctx, name)
	if err != nil {
		r.Log.Error(err, "could not get Machine owner's nodes", "kind", kind, "name", name, "namespace", namespace)
		return false, err
	}

	r.Log.Info("verifying nodes count restoration", "expected", replicas, "actual", len(nodes))
	return len(nodes) == replicas, nil
}

// getMachineOwner returns the MachineSet object given its name and namespace
func (r *MachineDeletionRemediationReconciler) getMachineOwner(ctx context.Context, kind, name, namespace string) (*unstructured.Unstructured, error) {
	kindToApiVersionMap := map[string]string{
		"MachineSet":             machinev1beta1.GroupVersion.String(),
		"ControlPlaneMachineSet": machinev1.GroupVersion.String(),
	}

	apiVersion, exists := kindToApiVersionMap[kind]
	if !exists {
		return nil, errors.Wrap(unrecoverableError, fmt.Sprintf("unknown kind %s", kind))
	}

	r.Log.Info("getting Machine owner", "kind", kind, "name", name, "namespace", namespace, "apiVersion", apiVersion)

	owner := &unstructured.Unstructured{}
	owner.SetKind(kind)
	owner.SetAPIVersion(apiVersion)
	key := client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}

	if err := r.Get(ctx, key, owner); err != nil {
		if apiErrors.IsNotFound(err) {
			return nil, errors.Wrap(unrecoverableError, err.Error())
		}
		return nil, err
	}
	return owner, nil
}

// getMachineOwnerNodes returns the Nodes associated to the Machine's Owner
func (r *MachineDeletionRemediationReconciler) getMachineOwnerNodes(ctx context.Context, ownerName string) ([]v1.Node, error) {
	allNodes := &v1.NodeList{}
	if err := r.List(ctx, allNodes); err != nil {
		return nil, err
	}

	machineOwnerNodes := []v1.Node{}
	for _, node := range allNodes.Items {
		machineName, machineNs, err := getMachineNameNsFromNode(&node)
		if err != nil {
			r.Log.Error(err, "could not get MachineNameNS from Node", "node", node.Name)
			continue
		}

		machine := machinev1beta1.Machine{}
		key := client.ObjectKey{
			Name:      machineName,
			Namespace: machineNs,
		}
		if err := r.Get(ctx, key, &machine); err != nil {
			r.Log.Error(err, "could not get Machine of Node", "node", node.Name, "search key", key)
			continue
		}

		if curOwnerName, _, err := getMachineOwnerNameKind(&machine); err != nil {
			r.Log.Error(err, "no valid ownerReference", "node", node.Name, "machine", machine.Name)
		} else if curOwnerName == ownerName {
			machineOwnerNodes = append(machineOwnerNodes, node)
		}
	}

	return machineOwnerNodes, nil
}

func (r *MachineDeletionRemediationReconciler) getMachineNameNsFromRemediationName(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation) (machineName, machineNs string, err error) {
	var node *v1.Node
	node, err = r.getNodeFromCR(ctx, remediation)
	if err != nil {
		if apiErrors.IsNotFound(err) {
			r.Log.Error(err, nodeNotFoundErrorMsg, "node name", remediation.Name)
			err = nodeNotFoundError
		}
		return "", "", err
	}

	machineName, machineNs, err = getMachineNameNsFromNode(node)
	if err != nil {
		r.Log.Error(err, "could not get Machine Name NS from Node", "node", node.Name, "annotations", node.GetAnnotations())
		return "", "", unrecoverableError
	}
	return machineName, machineNs, nil
}

func (r *MachineDeletionRemediationReconciler) getMachineNameNsFromOwnerReference(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation) (machineName, machineNs string) {
	for _, owner := range remediation.GetOwnerReferences() {
		if owner.Kind == "Machine" {
			machineName, machineNs = owner.Name, remediation.Namespace
			break
		}
	}

	return machineName, machineNs
}

func getMachineNameNsFromNode(node *v1.Node) (string, string, error) {
	var nodeAnnotations map[string]string
	if nodeAnnotations = node.Annotations; nodeAnnotations == nil {
		return "", "", fmt.Errorf(noAnnotationsError, node.Name)
	}

	var machineNameNs string
	if machineNameNs = nodeAnnotations[machineAnnotationOpenshift]; len(machineNameNs) == 0 {
		return "", "", fmt.Errorf(noMachineAnnotationError, node.Name)
	}

	if slice := strings.Split(machineNameNs, "/"); len(slice) == 2 {
		return slice[1], slice[0], nil
	}
	return "", "", fmt.Errorf(invalidValueMachineAnnotationError, node.Name)
}

// getMachineOwnerNameKind returns the Machine's ownerReference name and Kind. It returns an error if the Machine has
// more than one Owner Reference.
func getMachineOwnerNameKind(machine *machinev1beta1.Machine) (name, kind string, err error) {
	owners := machine.GetOwnerReferences()
	switch len(owners) {
	case 0:
		// not an error, Machines can be created without ownerReference
		return "", "", nil
	case 1:
		return owners[0].Name, owners[0].Kind, nil
	default:
		return "", "", fmt.Errorf("machine has more than one owner")
	}
}

// getMachineOwnerSpecReplicas returns the Machine's owner Spec.Replicas field
func (r *MachineDeletionRemediationReconciler) getMachineOwnerSpecReplicas(ctx context.Context, kind, name, namespace string) (int, error) {
	owner, err := r.getMachineOwner(ctx, kind, name, namespace)
	if err != nil {
		r.Log.Error(err, "could not get Machine owner", "kind", kind, "name", name, "namespace", namespace)
		if apiErrors.IsNotFound(err) {
			return 0, errors.Wrap(unrecoverableError, err.Error())
		}
		return 0, err
	}

	replicas, _, err := unstructured.NestedInt64(owner.Object, "spec", "replicas")
	if err != nil {
		return 0, errors.Wrap(unrecoverableError, err.Error())
	}
	return int(replicas), nil
}
