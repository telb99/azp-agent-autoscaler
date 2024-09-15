# Azure Pipeline Agent Autoscaler

azp-agent-autoscaler calls Azure Devops to automatically scale a Kubernetes stateful sets deployment of an Azure Pipelines agent. [A Helm chart for Azure Pipeline agents can be found here](https://github.com/ogmaresca/azp-agent), which also includes this app.

azp-agent-autoscaler should (in theory) work in Kubernetes versions that have the `apps/v1` API Versions of StatefulSets (Kubernetes 1.9+). It has been tested in Kubernetes versions 1.13-1.26.

## Installation

First, add this repo to Helm:

``` bash
helm repo add azp-agent-autoscaler https://raw.githubusercontent.com/ogmaresca/azp-agent-autoscaler/master/charts
helm repo update
```

Then use this command to install it:

``` bash
helm upgrade --install --namespace=azp azp-agent-autoscaler azp-agent-autoscaler/azp-agent-autoscaler --set 'azp.url=https://dev.azure.com/accountName,azp.token=AzureDevopsAccessToken,agents.Name=azp-agent'
```

You can find the limit on parallel jobs by going to your project settings in Azure Devops, clicking on Parallel jobs, and viewing your limit of self-hosted jobs.

## Configuration

The values `azp.token` and `azp.url` are required to install the chart. `azp.token` is the Personal Acces token to be used. This token requires Agent Pools permission (Read and Manage). `azp.url` is your Azure Devops URL, usually `https://dev.azure.com/<Your Organization>`.

`agents.Name` is the name of the resource your agents are deployed in. `agents.Namespace` is the namespace the resource is in, which defaults to the release namespace. `agents.Kind` is the resource kind the agents are deployed in. Only StatefulSet is currently supported, which is the default value.

| Parameter                           | Description                                                                                              | Default                                                           |
| ----------------------------------- | -------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| `nameOverride`                      | An override value for the name.                                                                          |                                                                   |
| `fullnameOverride`                  | An override value for the full name.                                                                     |                                                                   |
| `min`                               | The minimum number of free agent pods.                                                                   | 1                                                                 |
| `max`                               | The maximum number of agent pods.                                                                        | 100                                                               |
| `logLevel`                          | The log level (trace, debug, info, warn, error, fatal, panic)                                            | info                                                              |
| `rate`                              | Time to wait between polling Azure Devops and the Kubernetes API                                         | 20s                                                               |
| `scaleDownDelay`                    | The time to wait before being allowed to scale down after a scale up                                     | 15m                                                               |
| `scaleUpDelay`                      | The time to wait before being allowed to scale up after a scale down                                     | 5m                                                                |
| `agents.Kind`                       | The Kubernetes resource kind of the agents                                                               | StatefulSet                                                       |
| `agents.Name`                       | The Kubernetes resource name of the agents                                                               | ``                                                                |
| `agents.Namespace`                  | The Kubernetes resource namespace of the agents                                                          | `.Release.Namespace`                                              |
| `azp.url`                           | The Azure Devops account URL. ex: https://dev.azure.com/Organization                                     |                                                                   |
| `azp.token`                         | The Azure Devops access token.                                                                           |                                                                   |
| `azp.existingSecret`                | An existing secret that contains the token.                                                              |                                                                   |
| `azp.existingSecretKey`             | The key of the existing secret that contains the token.                                                  |                                                                   |
| `image.repository`                  | The Docker Hub repository of the agent autoscaler.                                                       | docker.io/gmaresca/azp-agent-autoscaler                           |
| `image.tag`                         | The image tag of the agent autoscaler.                                                                   | latest version                                                    |
| `image.pullPolicy`                  | The image pull policy.                                                                                   | IfNotPresent                                                      |
| `image.pullSecrets`                 | Image Pull Secrets to use.                                                                               | `[]`                                                              |
| `resources.requests.cpu`            | The CPU requests of the agent autoscaler.                                                                | 0.05                                                              |
| `resources.requests.memory`         | The memory requests of the agent autoscaler.                                                             | 16Mi                                                              |
| `resources.limits.cpu`              | The CPU limits of the agent autoscaler.                                                                  | 0.1                                                               |
| `resources.limits.memory`           | The memory limits of the agent autoscaler.                                                               | 32Mi                                                              |
| `livenessProbe.failureThreshold`    | The failure threshold for the liveness probe.                                                            | 3                                                                 |
| `livenessProbe.initialDelaySeconds` | The initial delay for the liveness probe.                                                                | 1                                                                 |
| `livenessProbe.periodSeconds`       | The liveness probe period.                                                                               | 10                                                                |
| `livenessProbe.successThreshold`    | The success threshold for the liveness probe.                                                            | 1                                                                 |
| `livenessProbe.timeoutSeconds`      | The timeout for the liveness probe.                                                                      | 1                                                                 |
| `minReadySeconds`                   | The deployment's `minReadySeconds`.                                                                      | 0                                                                 |
| `revisionHistoryLimit`              | Number of Deployment versions to keep.                                                                   | 10                                                                |
| `updateStrategy.type`               | The Deployment Update Strategy type.                                                                     | Recreate                                                          |
| `labels`                            | Labels to add to the Deployment.                                                                         | `{}`                                                              |
| `annotations`                       | Annotations to add to the Deployment.                                                                    | `{}`                                                              |
| `podLabels`                         | Labels to add to the Pod.                                                                                | `{}`                                                              |
| `podAnnotations`                    | Annotations to add to the Pod.                                                                           | `{}`                                                              |
| `pdb.enabled`                       | Whether to enable a PodDisruptionBudget.                                                                 | `false`                                                           |
| `pdb.minAvailable`                  | The minimum number of pods to keep. Incompatible with `maxUnavailable`.                                  | 50%                                                               |
| `pdb.maxUnavailable`                | The maximum unvailable pods. Incompatible with `minAvailable`.                                           | 50%                                                               |
| `rbac.create`                       | Whether to create Role Based Access for the deployment.                                                  | `true`                                                            |
| `rbac.psp.enabled`                  | Whether to create a PodSecurityPolicy for the deployment.                                                | `false`                                                           |
| `rbac.psp.name`                     | If set, the name of the PodSecurityPolicy to use, or create if `rbac.psp.enabled` is true.               |                                                                   |
| `rbac.psp.labels`                   | Labels to add to the PodSecurityPolicy.                                                                  | `{}`                                                              |
| `rbac.psp.annotations`              | Annotations to add to the PodSecurityPolicy.                                                             | `{}`                                                              |
| `rbac.psp.appArmorProfile`          | The AppArmor profile to use (if empty, AppArmor annotations will not be added to the PodSecurityPolicy). | runtime/default                                                   |
| `rbac.psp.seccompProfile`           | The Seccomp profile to use (if empty, seccomp annotations will not be added to the PodSecurityPolicy).   | runtime/default                                                   |
| `serviceAccount.create`             | Whether to create a service account for the deployment.                                                  | `true`                                                            |
| `serviceAccount.name`               | The name of an existing SA `serviceAccount.create` is false.                                             |                                                                   |
| `serviceAccount.labels`             | Labels to add to the ServiceAccount.                                                                     | `{}`                                                              |
| `serviceAccount.annotations`        | Annotations to add to the ServiceAccount.                                                                | `{}`                                                              |
| `serviceMonitor.enabled`            | Create a `prometheus-operator` ServiceMonitor                                                            | `false`                                                           |
| `serviceMonitor.namespace`          | The namespace to install the ServiceMonitor                                                              | Release namespace                                                 |
| `serviceMonitor.labels`             | Labels to add to the ServiceMonitor                                                                      | `{}`                                                              |
| `serviceMonitor.honorLabels`        | Set `honorLabels` on the ServiceMonitor spec                                                             |                                                                   |
| `serviceMonitor.interval`           | The scrape interval on the ServiceMonitor                                                                | Defaults to `rate`                                                |
| `serviceMonitor.metricRelabelings`  | `metricRelabelings` to set on the ServiceMonitor                                                         | `false`                                                           |
| `serviceMonitor.relabelings`        | `relabelings` to set on the ServiceMonitor                                                               | `false`                                                           |
| `grafanaDashboard.enabled`          | Create a ConfigMap with a Grafana dashboard.                                                             | `false`                                                           |
| `grafanaDashboard.labels`           | Labels to add to the Grafana dashboard ConfigMap.                                                        | `{"grafana_dashboard":"1"}`                                       |
| `rbac.getConfigmaps`                | Allow getting ConfigMaps, to retrieve the AZP_POOL env value.                                            | `false`                                                           |
| `rbac.getSecrets`                   | Allow getting Secrets, to retrieve the AZP_POOL env value.                                               | `false`                                                           |
| `dnsPolicy`                         | The pod DNS policy.                                                                                      | `null`                                                            |
| `dnsConfig`                         | The pod DNS config.                                                                                      | `{}`                                                              |
| `restartPolicy`                     | The pod restart policy.                                                                                  | Always                                                            |
| `nodeSelector`                      | The pod node selector.                                                                                   | `{}`                                                              |
| `tolerations`                       | The pod node tolerations.                                                                                | `{}`                                                              |
| `affinity`                          | The pod node affinity.                                                                                   | `{}`                                                              |
| `securityContext`                   | The pod security context.                                                                                | `{runAsUser:1000,runAsGroup:2000,fsGroup:3000,runAsNonRoot:true}` |
| `hostNetwork`                       | Whether to use the host network of the node.                                                             | `false`                                                           |
| `initContainers`                    | Init containers to add.                                                                                  | `[]`                                                              |
| `lifecycle`                         | Lifecycle (postStart, preStop) for the pod.                                                              | `{}`                                                              |
| `sidecars`                          | Additional containers to add.                                                                            | `[]`                                                              |


## Docker Hub

[View the Docker Hub page for azp-agent-autoscaler.](https://hub.docker.com/r/ogmaresca/azp-agent-autoscaler)
