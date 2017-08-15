# Creating a Kubernetes Deployment

## Summary

The application configuration is in the `config.yaml` Kubernetes secret,
which stores it in the secret `issecret`.

The deployment created in `deployment.yaml` loads the image from
quay.io/coreos/issue-sync, maps the `issecret` keys to environment
variables, and mounts the `jira_privatekey.pem` file in the current
directory as a file.


## Instructions

Copy your private key which you have configured with JIRA into the
current directory, and name it `jira_privatekey.pem`.

Fill out the config.yaml file with the correct configuration. Make sure
they are base64 encoded, for example with `echo -n "value" | base64 -w 0`,
where `value` is the configuration value you would like to encode.

Create the secret with

`kubectl --kubeconfig /path/to/kubeconfig create -f config.yaml`,

then the deployment with

`kubectl --kubeconfig /path/to/kubeconfig create -f deployment.yaml`.

It may be convenient to put kubeconfig into this folder.