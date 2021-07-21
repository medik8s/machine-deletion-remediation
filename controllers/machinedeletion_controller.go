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
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/machine-deletion/api/v1alpha1"
)

const (
	machineKind = "Machine"
)

// MachineDeletionReconciler reconciles a MachineDeletion object
type MachineDeletionReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=machine-deletion.medik8s.io,resources=machinedeletions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=machine-deletion.medik8s.io,resources=machinedeletions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=machine-deletion.medik8s.io,resources=machinedeletions/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MachineDeletion object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *MachineDeletionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("machinedeletion", req.NamespacedName)

	log.Info("reconciling...")

	//fetch the remediation
	var remediation *v1alpha1.MachineDeletion
	if remediation = r.getRemediation(ctx, req); remediation == nil {
		return ctrl.Result{}, nil
	}
	//not a machine based remediation
	machineOwnerRef := getMachineOwnerRef(remediation)
	if machineOwnerRef == nil {
		return ctrl.Result{}, nil
	}
	//delete the machine
	if err := r.deleteMachine(ctx, buildMachine(machineOwnerRef, remediation)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MachineDeletionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.MachineDeletion{}).
		Complete(r)
}

func (r *MachineDeletionReconciler) getRemediation(ctx context.Context, req ctrl.Request) *v1alpha1.MachineDeletion {
	remediation := new(v1alpha1.MachineDeletion)
	key := client.ObjectKey{Name: req.Name, Namespace: req.Namespace}
	if err := r.Client.Get(ctx, key, remediation); err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "error retrieving remediation %s in namespace %s: %v", req.Name, req.Namespace)
		}
		return nil
	}
	return remediation
}

func (r *MachineDeletionReconciler) deleteMachine(ctx context.Context, machine *unstructured.Unstructured) error {
	if err := r.Client.Delete(ctx, machine); err != nil {
		if !errors.IsNotFound(err) {
			r.Log.Error(err, "error deleting machine %s in namespace %s: %v", machine.GetName(), machine.GetNamespace())
			return err
		}
		r.Log.Info("machine: %s in namespace: %s is deleted , but remediation still exist", machine.GetName(), machine.GetNamespace())
	}
	return nil
}

func getMachineOwnerRef(remediation *v1alpha1.MachineDeletion) *v1.OwnerReference {
	for _, ownerRef := range remediation.OwnerReferences {
		if ownerRef.Kind == machineKind {
			return &ownerRef
		}
	}
	return nil
}
func buildMachine(ref *v1.OwnerReference, remediation *v1alpha1.MachineDeletion) *unstructured.Unstructured {
	machine := new(unstructured.Unstructured)
	machine.SetName(remediation.Name)
	machine.SetNamespace(remediation.Namespace)
	machine.SetKind(machineKind)
	machine.SetAPIVersion(ref.APIVersion)
	return machine
}
