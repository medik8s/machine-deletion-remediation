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

	"github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"

	"github.com/medik8s/machine-deletion-remediation/api/v1alpha1"
)

const (
	machineAnnotationOpenshift = "machine.openshift.io/machine"
	machineKind                = "Machine"
	machineSetKind             = "MachineSet"
	nhcTimeOutAnnotation       = "remediation.medik8s.io/nhc-timed-out"
	// MachineNameNsAnnotation contains to-be-deleted Machine's Name and Namespace
	MachineNameNsAnnotation = "machine-deletion-remediation.medik8s.io/machineNameNamespace"
	// Infos
	postponedMachineDeletionInfo  = "node-associated machine was not deleted yet"
	successfulMachineDeletionInfo = "node-associated machine correctly deleted"
	//Errors
	noAnnotationsError                 = "failed to find machine annotation on node name: %s"
	noMachineAnnotationError           = "failed to find openshift machine annotation on node name: %s"
	invalidValueMachineAnnotationError = "failed to extract Machine Name and Machine Namespace from machine annotation on the node for node name: %s"
	failedToDeleteMachineError         = "failed to delete machine of node name: %s"
	noNodeFoundError                   = "failed to fetch node"
	noMachineFoundError                = "failed to fetch machine of node"
)

type machineDeletionRemediationState string

type processingChangeReason string

const (
	remediationStarted         processingChangeReason = "RemediationStarted"
	remediationTerminatedByNHC processingChangeReason = "RemediationStoppedByNHC"
	remediationFinished        processingChangeReason = "RemediationFinished"
	remediationFailed          processingChangeReason = "RemediationFailed"
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
	var remediation *v1alpha1.MachineDeletionRemediation
	if remediation, err = r.getRemediation(ctx, req); remediation == nil || err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Machine Deletion Remediation CR found", "name", remediation.GetName())

	defer func() {
		if updateErr := r.updateStatus(ctx, remediation); updateErr != nil {
			if !apiErrors.IsConflict(updateErr) {
				finalErr = utilerrors.NewAggregate([]error{updateErr, finalErr})
			}
			finalResult.RequeueAfter = time.Second
		}
	}()

	// Remediation's name was created from Node's name
	nodeName := remediation.GetName()

	if r.isStoppedByNHC(remediation) {
		log.Info("NHC stop requested")
		r.updateConditions(remediationTerminatedByNHC, remediation)
		return ctrl.Result{}, nil
	}

	if updateRequired, err := r.updateConditions(remediationStarted, remediation); err != nil {
		log.Error(err, "could not update Status conditions")
	} else if updateRequired {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	var machine *unstructured.Unstructured
	if machine, err = r.getMachine(remediation); err != nil {
		if apiErrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Unexpected error fetching Machine from Node", "node", nodeName)
		return ctrl.Result{}, err
	}

	log.Info("node-associated machine found", "node", remediation.Name, "machine", machine.GetName())

	if !machine.GetDeletionTimestamp().IsZero() {
		// Machine deletion requested already.
		// Log deletion progress until the Machine exists
		log.Info(postponedMachineDeletionInfo, "node", nodeName, "machine", machine.GetName(), "machine status.phase", getMachineStatusPhase(machine))
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	if !hasControllerOwner(machine) {
		log.Info("ignoring remediation of node-associated machine: the machine has no controller owner", "machine", machine.GetName(), "node name", remediation.Name)
		return ctrl.Result{}, nil
	}

	// save Machine's name and namespace to follow its deletion phase
	if err = r.saveMachineNameNs(ctx, remediation, machine.GetName(), machine.GetNamespace()); err != nil {
		log.Error(err, "could not save Machine's Name and Namespace", "machine name", machine.GetName(), "machine namespace", machine.GetNamespace())
		return ctrl.Result{}, errors.Wrapf(err, "failed to save Machine's name and namespace")
	}

	log.Info("request node-associated machine deletion", "machine", machine.GetName(), "node", nodeName)
	err = r.Delete(ctx, machine)
	if err != nil {
		log.Error(err, "failed to delete machine associated to node", "machine", machine.GetName(), "node", nodeName)
		return ctrl.Result{}, err
	}

	// requeue immediately to check machine deletion progression
	return ctrl.Result{Requeue: true}, nil
}

func hasControllerOwner(machine *unstructured.Unstructured) bool {
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
	remediation := new(v1alpha1.MachineDeletionRemediation)
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

func (r *MachineDeletionRemediationReconciler) getNodeFromMdr(mdr *v1alpha1.MachineDeletionRemediation) (*v1.Node, error) {
	node := &v1.Node{}
	key := client.ObjectKey{
		Name: mdr.Name,
	}

	if err := r.Get(context.Background(), key, node); err != nil {
		return nil, err
	}
	return node, nil
}

func (r *MachineDeletionRemediationReconciler) getMachine(remediation *v1alpha1.MachineDeletionRemediation) (*unstructured.Unstructured, error) {
	machineName, machineNs, err := getMachineNameNsFromRemediation(remediation)
	if err != nil {
		r.Log.Error(err, "could not get Machine data from remediation", "remediation", remediation.GetName(), "annotation", MachineNameNsAnnotation)
		return nil, err
	}

	var gotMachineFromNode bool
	if machineName == "" {
		// Remediation does not have the MachineNameNsAnnotation yet.
		// Get the Machine's data from its Node.
		node, err := r.getNodeFromMdr(remediation)
		if err != nil {
			r.Log.Error(err, noNodeFoundError, "node name", remediation.Name)
			return nil, err
		}
		if machineName, machineNs, err = getMachineNameNsFromNode(node); err != nil {
			r.Log.Error(err, "could not get Machine Name NS from Node", "node", node.Name, "annotations", node.GetAnnotations())
			return nil, err
		}

		gotMachineFromNode = true
	}

	machine := new(unstructured.Unstructured)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(v1beta1.SchemeGroupVersion.String())

	key := client.ObjectKey{
		Name:      machineName,
		Namespace: machineNs,
	}

	if err := r.Get(context.TODO(), key, machine); err != nil {
		if apiErrors.IsNotFound(err) {
			if gotMachineFromNode {
				// The Machine's Name and Ns came from the related Node, it was not expected not to find the Machine
				r.Log.Error(err, noMachineFoundError, "node", remediation.Name, "machine", machineName)
				r.updateConditions(remediationFailed, remediation)
			} else {
				// The Machine's Name and Ns came from CR annotation, the Machine might have been deleted upon our request
				r.Log.Info(successfulMachineDeletionInfo, "node", remediation.Name, "machine", machineName)
				r.updateConditions(remediationFinished, remediation)
			}
		}
		return nil, err
	}
	return machine, nil
}

// saveMachineNameNs saves Machine Name and Namespace in a remediation's annotation

func (r *MachineDeletionRemediationReconciler) saveMachineNameNs(ctx context.Context, remediation *v1alpha1.MachineDeletionRemediation, machineName, machineNamespace string) error {
	annotations := remediation.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	} else if _, exists := annotations[MachineNameNsAnnotation]; exists {
		return nil
	}

	annotations[MachineNameNsAnnotation] = fmt.Sprintf("%s/%s", machineNamespace, machineName)
	remediation.SetAnnotations(annotations)

	return r.Update(ctx, remediation)
}

func getMachineNameNsFromRemediation(remediation *v1alpha1.MachineDeletionRemediation) (name, namespace string, err error) {
	annotations := remediation.GetAnnotations()
	nameNs, exists := annotations[MachineNameNsAnnotation]
	if !exists {
		return "", "", nil
	}

	name, namespace, err = extractNameAndNamespace(nameNs, remediation.GetName())
	if err != nil {
		msg := "could not get Machine data from remediation '%s' annotation '%s': error %w"
		return "", "", fmt.Errorf(msg, remediation.GetName(), nameNs, err)
	}
	return name, namespace, err
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

// updateConditions updates the status conditions of a MachineDeletionRemediation object based on the provided processingChangeReason.
// note that it does not update server copy of MachineDeletionRemediation object
// return a boolean, indicating if the Status Condition needed to be updated or not, and an error if an unknown processingChangeReason is provided
func (r *MachineDeletionRemediationReconciler) updateConditions(reason processingChangeReason, mdr *v1alpha1.MachineDeletionRemediation) (bool, error) {
	var processingConditionStatus, succeededConditionStatus metav1.ConditionStatus

	switch reason {
	case remediationStarted:
		processingConditionStatus = metav1.ConditionTrue
		succeededConditionStatus = metav1.ConditionUnknown
	case remediationFinished:
		processingConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionTrue
	case remediationTerminatedByNHC, remediationFailed:
		processingConditionStatus = metav1.ConditionFalse
		succeededConditionStatus = metav1.ConditionFalse
	default:
		err := fmt.Errorf("unknown processingChangeReason:%s", reason)
		r.Log.Error(err, "couldn't update MDR Status Conditions")
		return false, err
	}

	// if ProcessingConditionType is already false, it cannot be changed to true again
	if processingConditionStatus == metav1.ConditionTrue &&
		meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, v1alpha1.ProcessingConditionType, metav1.ConditionFalse) {
		return false, nil
	}

	// if the requested Status.Conditions are already set, skip update
	if meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, v1alpha1.ProcessingConditionType, processingConditionStatus) &&
		meta.IsStatusConditionPresentAndEqual(mdr.Status.Conditions, v1alpha1.SucceededConditionType, succeededConditionStatus) {
		return false, nil
	}

	r.Log.Info("updating Status Condition", "processingConditionStatus", processingConditionStatus, "succededConditionStatus", succeededConditionStatus, "reason", string(reason))
	meta.SetStatusCondition(&mdr.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.ProcessingConditionType,
		Status: processingConditionStatus,
		Reason: string(reason),
	})

	meta.SetStatusCondition(&mdr.Status.Conditions, metav1.Condition{
		Type:   v1alpha1.SucceededConditionType,
		Status: succeededConditionStatus,
		Reason: string(reason),
	})

	return true, nil
}

// isStoppedByNHC checks if NHC requested to stop the remediation
func (r *MachineDeletionRemediationReconciler) isStoppedByNHC(remediation *v1alpha1.MachineDeletionRemediation) bool {
	if remediation != nil && remediation.Annotations != nil && remediation.DeletionTimestamp == nil {
		_, isTimeoutIssued := remediation.Annotations[nhcTimeOutAnnotation]
		return isTimeoutIssued
	}
	return false
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

func extractNameAndNamespace(nameNs string, nodeName string) (string, string, error) {
	if nameNsSlice := strings.Split(nameNs, "/"); len(nameNsSlice) == 2 {
		return nameNsSlice[1], nameNsSlice[0], nil
	}
	return "", "", fmt.Errorf(invalidValueMachineAnnotationError, nodeName)
}

func getMachineStatusPhase(machine *unstructured.Unstructured) string {
	log := ctrl.Log.WithName("getMachineStatusPhase")

	status, exists, err := unstructured.NestedMap(machine.Object, "status")
	if err != nil {
		log.Error(err, "could not get machine's status", "machine", machine.GetName())
		return "unknown"
	}
	if !exists {
		log.Info("machine does not have status", "machine", machine.GetName())
		return "unknown"
	}

	phase, exists, err := unstructured.NestedString(status, "phase")
	if err != nil {
		log.Error(err, "could not get machine's status", "machine", machine.GetName())
		return "unknown"
	}
	if !exists {
		log.Info("machine does not have phase", "machine", machine.GetName())
		return "unknown"
	}
	return phase
}
