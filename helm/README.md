# MTA-STS helm chart
Custom helm chart to deploy a nginx server that serves the [MTA-STS policy](https://datatracker.ietf.org/doc/html/rfc8461). This serves as a simple example deployment that still has some purpose.

## Variables
* replicaCount: numer of replicas that should be deployed
* domain: Domain name for which the MTA-STS policy should be served. An ingress rule is registered for https://mta-sts.{{domain}}/.well-known/mta-sts.txt.
* mta_sts
  * mode: possible values are none, testing or enforce. 
  * max_age: max lifetime of the policy in seconds max value is according to the [RFC](https://datatracker.ietf.org/doc/html/rfc8461) 31536000 (1 year)
  * mx_records: list of mx-entries that are valid for the domain (the domains has to be configured on the ingress level which is not part of this helm chart)
