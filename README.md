# cloud-pv-admission-labeler

Admission webhook to add topology labels (zones/regions) to PersistentVolumes from cloud providers.
This replaces the deprecated `PersistentVolumeLabel` admission controller in kube-apiserver.

## Setup

The steps below use the GCE based installation in `manifest/gce.yaml`. Use manifest for other cloud providers based on your cloud provider (e.g. `manifest/aws.yaml`, `manifest.azure.yaml`, etc).

### Generate certificates

Generate the CA key:
```
$ openssl genrsa -out ca.key 2048
```

Generate the CA cert:
```
$ openssl req -x509 -new -nodes -key ca.key -subj "/CN=cloud-pv-admission-labeler.kube-system.svc" -days 10000 -out ca.crt
```

Generate the server key:
```
$ openssl genrsa -out server.key 2048
```

Generate the server CSR:
```
$ openssl req -new -key server.key -out server.csr
```

Generate the server certificate:
```
$ openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key  -CAcreateserial -out server.crt -days 10000  -extfile <(printf "subjectAltName=DNS:cloud-pv-admission-labeler.kube-system.svc") -sha256
```

### Add certificates to manifests

```
$ sed -i "s|__CA_CERT__|$(cat ./certs/ca.crt | base64 -w0)|g" manifests/gce.yaml
$ sed -i "s|__SERVER_CERT__|$(cat ./certs/server.crt | base64 -w0)|g" manifests/gce.yaml
$ sed -i "s|__SERVER_KEY__|$(cat ./certs/server.key | base64 -w0)|g" manifests/gce.yaml
```

###  Deploy webhook

```
$ kubectl apply -f manifests/gce.yaml
```

## Community, discussion, contribution, and support

Learn how to engage with the Kubernetes community on the [community page](http://kubernetes.io/community/).

You can reach the maintainers of this project at:

- [Slack channel](https://slack.k8s.io/)
- [Mailing list](https://groups.google.com/forum/#!forum/kubernetes-sig-cloud-provider)

### Code of conduct

Participation in the Kubernetes community is governed by the [Kubernetes Code of Conduct](code-of-conduct.md).

[owners]: https://git.k8s.io/community/contributors/guide/owners.md
[Creative Commons 4.0]: https://git.k8s.io/website/LICENSE
