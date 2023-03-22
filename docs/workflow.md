## NodeHealthCheck life-cycle

When a node is unhealthy:
- sum up how many other nodes are unhealthy.
- if the number of healthy nodes > minHealthy the controllers creates the external remediation object
- the external remediation object has an OwnerReference on the NodeHeathCheck object
- controller updates the NodeHealthCheck.Status

When a node turns healthy:
- the controller deletes the external remediation object
- the controller updates the NodeHealthCheck.Status

## Remediation Providers responsibility

It is upto the remediation provider to delete the external remediation object if the node is deleted and another is
reprovisioned. In that specific scenario the controller can not assume a successful node remediation because the
node with that name doesn't exist, and instead there will be a new one.
