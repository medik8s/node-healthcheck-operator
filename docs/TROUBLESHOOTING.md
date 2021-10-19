# Node is not being remediated

The operator makes few checks before eventually deciding to create a remediation object.

## Minimum healthy nodes in the selection set
Given enough healthy nodes a faulty node will be remediated. However, the number of healthy nodes is calculated against
the total number of hosts selected by the selector in the resource, and not total in the system.
For our default NHC this is easy to forget about, because the selector selects all worker hosts, effectively.
For a more elaborated selector, the admin should put more thoughts into it.
A too narrow selector will have low chances of remediation.

Check the events on the respected NHC object to spot events like this:

```
  Warning  RemediationSkipped  4s (x6 over 2m51s)    NodeHealthCheck  Skipped remediation because the number of healthy nodes selected by the selector is 0 and should equal or exceed 1
```

## Pause requests
Each NHC can be directed to pause any new remediations for the selection set. If there is any pause 
requests in the list the resulting event is emitted:

```
  Normal   RemediationSkipped  42m (x5 over 45m)     NodeHealthCheck  Skipping remediation because there are pause requests
```

## Cluster Updates
The operator can detect when a cluster is updating (OpenShift only at the moment), and when it does all
new remediations are skipped. Event emitted:

```
  Normal   RemediationSkipped  42m (x5 over 45m)     NodeHealthCheck  Skipping remediation because the cluster is upgrading
```


