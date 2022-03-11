# Ingress helm chart
Helm chart to deploy a the golang ingress to serve as an entry point.
For the given host names a master ingress configuration without backend routing is provided to prefetch the certificates via [cert-manager](https://cert-manager.io/) from [Let's Encrypt](https://letsencrypt.org/).
As cert-manager is used for certificate handling, it has to be installed before this chart can be deployed. Like so (assumming the namespace ingress exists):
```
helm install cert-manager jetstack/cert-manager -f values-cert-manager.yml
helm install ingress . -n ingress -f values.yml -f ../../terraform/out/helm_values.yml
```

## Variables
* replicaCount: number of replicas for the ingress-controller
* ingressClassName: Ingress class name for the new ingress-controller
* issuer:
  * email: E-Mail as contact for LetsEncrypt for relevant informations about certificate renewal etc.
* domains:
  * names: For both common_name and subject_alternative_names a mergeable ingress master is setup. Hence, collisions are not supported.
    * common_name: The common name of the certificate.
    * subject_alternative_names: List of subjec alternative names for the certificate. 
  * letsencrypt_prod: Boolean whether the LetsEncrypt prod or staging CA should be used. The productive LetsEncrypt endpoint issues the actual browser supported certificated, but has rather strict [rate limits](https://letsencrypt.org/docs/rate-limits/) and should not be used for testing purposes.
  * robots: Whether to automatically setup up a minimalistic nginx that serves a /robots.txt-file.
    * entries: List of entries for the robots.txt
      * user_agent: User-agent entry
      * Allow: List of Allow entries
      * Disallow: List of Disallow entries
    * sitemap: List of sitemap entries
    * replicaCount: Replica-Count of the aforementioned nginx deployment. Defaults to 1.
