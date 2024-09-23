package main

import (
	"flag"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/ogmaresca/azp-agent-autoscaler/pkg/args"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/azuredevops"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/health"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/kubernetes"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/logging"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/math"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/scaling"
)

const poolNameEnvVar = "AZP_POOL"

func main() {
	// Parse arguments
	flag.Parse()

	if err := args.ValidateArgs(); err != nil {
		panic(err.Error())
	}
	args := args.ArgsFromFlags()

	logging.Logger.SetLevel(args.Logging.Level)

	logging.Logger.Info("Main: starting up")

	// Initialize Azure Devops and Kubernetes clients
	azdClient := azuredevops.MakeClient(args.AZD.URL, args.AZD.Token)
	k8sClient, err := kubernetes.MakeClient()
	if err != nil {
		panic(err.Error())
	}

	deploymentChan := make(chan kubernetes.WorkloadReturn)
	verifyHPAChan := make(chan error)
	agentPoolsChan := make(chan azuredevops.PoolDetailsResponse)

	// Get AZP agent workload
	go k8sClient.GetWorkloadAsync(deploymentChan, args.Kubernetes)
	// Verify there isn't a HorizontalPodAutoscaler
	go k8sClient.VerifyNoHorizontalPodAutoscalerAsync(verifyHPAChan, args.Kubernetes)
	// Get all agent pools
	go azdClient.ListPoolsAsync(agentPoolsChan)

	// Setup the web server to respond to health checks and metrics requests
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/healthz", health.LivenessCheck{})
		mux.Handle("/metrics", promhttp.Handler())
		err := http.ListenAndServe(fmt.Sprintf(":%d", args.Health.Port), mux)
		if err != nil {
			logging.Logger.Panicf("Error serving health checks and metrics: %s", err.Error())
		}
	}()

	// Retrieve channel results
	deployment := <-deploymentChan
	if deployment.Err != nil {
		logging.Logger.Panicf("Error retrieving %s in namespace %s: %s", args.Kubernetes.FriendlyName(), args.Kubernetes.Namespace, deployment.Err.Error())
	}
	if err := <-verifyHPAChan; err != nil {
		logging.Logger.Panic(err.Error())
	}
	agentPools := <-agentPoolsChan
	if agentPools.Err != nil {
		logging.Logger.Panicf("Error retrieving agent pools: %s", agentPools.Err.Error())
	} else if len(agentPools.Pools) == 0 {
		logging.Logger.Panic("Error - did not find any agent pools")
	}

	// Discover the pool name from the environment variables
	agentPoolName, err := k8sClient.Sync().GetEnvValue(deployment.Resource.PodTemplateSpec.Spec, deployment.Resource.Namespace, poolNameEnvVar)
	if err != nil {
		logging.Logger.Panicf("Could not retrieve environment variable %s from %s: %s", poolNameEnvVar, deployment.Resource.FriendlyName, err)
	}

	var agentPoolID *int
	for _, agentPool := range agentPools.Pools {
		if !agentPool.IsHosted && agentPool.Name == agentPoolName {
			agentPoolID = &agentPool.ID
			break
		}
	}
	if agentPoolID == nil {
		logging.Logger.Panicf("Error - could not find an agent pool with name %s", agentPoolName)
	}

	logging.Logger.Infof("Found ADO Agent Pool %s (ID %d) from %s", agentPoolName, *agentPoolID, deployment.Resource.FriendlyName)

	lastAutoscaleMsg := ""
	lastMessageLogged := time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	for {
		msg, err := scaling.Autoscale(azdClient, *agentPoolID, k8sClient, deployment.Resource, args)
		if err != nil {
			switch t := err.(type) {
			case azuredevops.HTTPError:
				httpError := err.(azuredevops.HTTPError)
				if httpError.RetryAfter != nil {
					logging.Logger.Warnf("%s %s", t, httpError.Error())
					timeToSleep := math.MaxDuration(*httpError.RetryAfter, args.Rate)
					logging.Logger.Infof("Retrying after %s", timeToSleep.String())
					time.Sleep(timeToSleep)
					continue
				}
			}
			logging.Logger.Panicf("Error autoscaling statefulset/%s: %s - %s", deployment.Resource.Name, msg, err.Error())
		} else if msg != lastAutoscaleMsg || time.Now().After(lastMessageLogged.Add(15*time.Minute)) || logging.Logger.IsLevelEnabled(log.DebugLevel) {
			lastMessageLogged = time.Now()
			logging.Logger.Info(msg)
			lastAutoscaleMsg = msg
			time.Sleep(args.Rate)
		}
	}
}
