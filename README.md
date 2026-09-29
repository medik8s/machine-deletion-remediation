# Machine-API Driven Remediation

This operator conforms to the External Remediation of [NodeHealthCheck](https://github.com/medik8s/node-healthcheck-operator#readme) and is designed to work with [Node Health Check]((https://github.com/medik8s/node-healthcheck-operator#readme)) to reprovision unhealthy nodes using the [Machine API](https://github.com/openshift/machine-api-operator#readme). It functions by following the annotation on the Node to the associated Machine object, confirms that it has an owning controller (e.g. MachineSetController), and deletes it.  Once the Machine CR has been deleted, the owning controller creates a replacement. 

## Pre-requisites
* Machine API based cluster that is able to programmatically destroy and create cluster nodes
* Nodes are associated with Machines
* Machines are declaratively managed
* Node Health Check is installed and running 

## Installation
- Deploy MDR (Machine-deletion-remediation) to a container in the cluster pod. Try `make deploy`, official images coming soon.
- Load the yaml manifest of the MDR template (see below).
- Modifying NodeHealthCheck CR to use MDR as it's remediator.
This is basically a specific use case of an External Remediation of [NodeHealthCheck](https://github.com/medik8s/node-healthcheck-operator#readme).
In order to set up: make sure that Node Health Check is running, Machine-deletion-remediation controller exists and then create the necessary CRs.

### Deploying the current source to OpenShift

For PR or branch testing, build the operator with the pinned source-deployment
toolchain, push temporary images to `ttl.sh`, and install the generated OLM
bundle with operator-sdk:

```bash
make dev-olm-deploy
```

The temporary images expire after two hours by default. Override the duration
and deployment namespace when needed, for example:

```bash
TTL_SH_TTL=4h DEV_OLM_OPERATOR_NAMESPACE=openshift-workload-availability make dev-olm-deploy
make dev-olm-undeploy
```

## Example CRs
An example MDR template object.
```yaml
   apiVersion: machine-deletion-remediation.medik8s.io/v1alpha1
   kind: MachineDeletionRemediationTemplate
   metadata:
     name: group-x
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
    kind: MachineDeletionRemediationTemplate
    apiVersion: machine-deletion-remediation.medik8s.io/v1alpha1
    name: group-x
    namespace: default
```
While the admin may define many NodeHealthCheck domains, they can all use the same MDR template if desired.


An example remediation request for Node `worker-0-21` (NOTE: *uid* is the nodehealthcheck-sample's UID).
```yaml
apiVersion: machine-deletion-remediation.medik8s.io/v1alpha1
kind: MachineDeletionRemediation
metadata:
  name: worker-0-21
  namespace: default
spec: {}
```
These CRs are created by NodeHealthCheck when it detects a failed node. 
The MDR operator watches for them to be created, looks up the Machine CR and deletes Node associated with it.
MDR CRs are deleted by NodeHealthCheck when it sees the Node is healthy again. 
