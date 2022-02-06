# Machine-API Driven Remediation

This operator conforms to the External Remediation of [NodeHealthCheck](https://github.com/medik8s/node-healthcheck-operator#readme)
and is designed to work with [Node Health Check]((https://github.com/medik8s/node-healthcheck-operator#readme)) to reprovision unhealthy
nodes using the [Machine API](https://github.com/openshift/machine-api-operator#readme). It functions by following the annotation on
the Node to the associated Machine object, confirms that it has an owning controller (e.g. MachineSetController), and deletes it.
Once the Machine CR has been deleted, the owning controller creates a replacement. 

## Pre-requisites
* Machine API based cluster that is able to programmatically destroy and create cluster nodes
* Nodes are associated with Machines
* Machines are declaratively managed
* Node Healthheck is installed and running 

## Installation
- Deploy MDR (Machine-deletion-remediation) to a container in the cluster pod.  Try `make deploy`, official images comming soon.
- Load the yaml manifest of the MDR template (see below).
- Modifying NodeHealthCheck CR to use MDR as it's remediator.
This is basically a specific use case of an External Remediation of [NodeHealthCheck](https://github.com/medik8s/node-healthcheck-operator#readme).
In order to set up: make sure that Node Health Check is running, Machine-deletion-remediation controller exists and then create the necessary CRs.

## Example CRs
An example MDR template object.
```yaml
   apiVersion: machine-deletion.medik8s.io/v1alpha1
   kind: MachineDeletionTemplate
   metadata:
     name: my-name
     namespace: default
   spec:
     template:
       spec: {}
```
These CRs are created by the admin and are used as a template by NodeHealthCheck for creating the CRs that represent a request for a Node to be recovered.

Configuring NodeHealthCheck to use the example `group-x` template above.
```yaml
apiVersion: remediation.medik8s.io/v1alpha1
kind: NodeHealthCheck
metadata:
  name: nodehealthcheck-sample
spec:
  remediationTemplate:
    apiVersion: machine-deletion.medik8s.io/v1alpha1
    kind: MachineDeletionTemplate
    name: my-name
    namespace: default
```
While the admin may define many NodeHealthCheck domains, they can all use the same MDR template if desired.


An example remediation request for Node `worker-0-21`.
```yaml
apiVersion: machine-deletion.medik8s.io/v1alpha1
kind: MachineDeletion
metadata:
  name: worker-0-21
  namespace: default
  ownerReferences:
    - kind: NodeHealthCheck
      apiVersion: remediation.medik8s.io/v1alpha1
      name: nodehealthcheck-sample
spec: {}
```
These CRs are created by NodeHealthCheck when it detects a failed node. 
The MDR operator watches for them to be created, looks up the Machine CR and deletes Node associated with it.
MDR CRs are deleted by NodeHealthCheck when it sees the Node is healthy again. 
