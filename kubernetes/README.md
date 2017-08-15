# Creating a Kubernetes Deployment

## Instructions

Copy your private key which you have configured with JIRA into the
current directory.

Fill out the config.yaml and secret.yaml files with the correct
configuration. These are the same keys which are used in the application
command line parameters or configuration file, as noted in the main
README. Make sure the values in the secret are base64 encoded, for
example with `echo -n "value" | base64 -w 0`, where `value` is the
configuration value you would like to encode.

Create the secret and configmap with

```
kubectl --kubeconfig /path/to/kubeconfig create -f secret.yaml
kubectl --kubeconfig /path/to/kubeconfig create -f config.yaml
```

then create the private key secret with

```
kubectl --kubeconfig /path/to/kubeconfig create secret generic privatekey --from-file privatekey.pem
```

where `privatekey.pem` can be replaced with the name of your private key
file, as it is in secret.yaml.

Finally, create the deployment with

```
kubectl --kubeconfig /path/to/kubeconfig create -f deployment.yaml
```
