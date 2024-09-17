package scaling

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/ogmaresca/azp-agent-autoscaler/pkg/args"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/azuredevops"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/collections"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/kubernetes"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/logging"
	"github.com/ogmaresca/azp-agent-autoscaler/pkg/math"
)

var (
	lastScaleDown    = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastScaleUp      = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	scaleDownCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "azp_agent_autoscaler_scale_down_count",
		Help: "The total number of scale downs",
	})
	scaleUpCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "azp_agent_autoscaler_scale_up_count",
		Help: "The total number of scale ups",
	})
	scaleDownLimitedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "azp_agent_autoscaler_scale_down_limited_count",
		Help: "The total number of scale downs prevented due to limits",
	})
	scaleUpLimitedCounter = promauto.NewCounter(prometheus.CounterOpts{
		Name: "azp_agent_autoscaler_scale_up_limited_count",
		Help: "The total number of scale ups prevented due to limits",
	})
	scaleSizeGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_scale_size",
		Help: "The size of the agent scaling",
	})
	totalAgentsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_total_agents_count",
		Help: "The total number of agents",
	})
	activeAgentsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_active_agents_count",
		Help: "The number of active agents",
	})
	pendingAgentsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_pending_agents_count",
		Help: "The number of pending agents",
	})
	failedAgentsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_failed_agents_count",
		Help: "The number of failed agents",
	})
	queuedPodsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_queued_pods_count",
		Help: "The number of queued pods",
	})
)

// Reset the auto scale state - for testing purposes
func Reset() {
	lastScaleDown = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastScaleUp = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
}

// Autoscale the agent deployment
func Autoscale(azdClient azuredevops.ClientAsync, agentPoolID int, k8sClient kubernetes.ClientAsync, deployment *kubernetes.Workload, args args.Args) error {
	logging.Logger.Debugf("----- auto scale start: lastScaleDown: %s, lastScaleUp: %s -----", lastScaleDown.String(), lastScaleUp.String())

	// Get active agents, queued jobs, and pods
	agents, jobs, pods, err := getAgentsJobsAndPods(azdClient, agentPoolID, k8sClient, deployment)
	if err != nil {
		return err
	}

	// Get all pod names and statuses
	podCount, podNames, runningPodCount, pendingPodCount, unschedulablePodCount, failedPodCount := analyzePods(pods)

	// Get number of active agents
	activeAgentNames := getActiveAgentNames(agents.Agents, podNames)
	activeAgentCount := int32(len(activeAgentNames))

	// Determine the number of jobs that are queued
	queuedJobCount := getQueuedJobCount(jobs.Jobs, activeAgentNames)

	logging.Logger.Debugf("Found %d active agents and %d queued jobs.", activeAgentCount, queuedJobCount)

	// Update metrics
	totalAgentsGauge.Set(float64(podCount))
	activeAgentsGauge.Set(float64(activeAgentCount))
	pendingAgentsGauge.Set(float64(pendingPodCount))
	failedAgentsGauge.Set(float64(failedPodCount))
	queuedPodsGauge.Set(float64(queuedJobCount))

	if runningPodCount != podCount {
		if unschedulablePodCount != pendingPodCount || failedPodCount != 0 {
			logging.Logger.Infof("Not scaling - there are %d pending pods and %d failed pods.", pendingPodCount, failedPodCount)
			scaleSizeGauge.Set(0)
			return nil
		}
	}

	// Calculate number of pods to scale to taking into account min and max limits
	scalingDirection, podsToScaleTo := calculatePodsToScaleTo(activeAgentCount, queuedJobCount, podCount, args.Min, args.Max)
	logging.Logger.Debugf("podsToScaleTo: %d, scalingDirection: %d", podsToScaleTo, scalingDirection)

	if scalingDirection < 0 {
		// avoid scaling down too soon after a scale up
		now := time.Now()
		nextAllowedScaleDown := lastScaleUp.Add(args.ScaleDown.Delay)
		if now.Before(nextAllowedScaleDown) {
			logging.Logger.Debugf("Not scaling down %s from %d - cannot scale down until %s", deployment.FriendlyName, podCount, nextAllowedScaleDown.String())
			scaleDownLimitedCounter.Inc()
			scaleSizeGauge.Set(0)
			return nil
		}

		// get the agent that will be deleted if we scale down
		agentNameToDelete := fmt.Sprintf("%s-%d", deployment.Name, podsToScaleTo)
		agentToDelete := getAgentDetails(agents, agentNameToDelete)

		// if it is enabled, disable it so that it won't pick up more jobs
		if len(agentToDelete) == 1 && agentToDelete[0].Enabled {
			if args.Action == "dry-run" {
				logging.Logger.Infof("Dry Run: would have disabled %s", agentNameToDelete)
			} else {
				// if the pod that will be deleted when we scale down exists and is enabled, disable it and return...
				// we don't want to scaledown if there's a chance the agent pod can pick up a job before the scale down happens
				logging.Logger.Debugf("Disabling agent: %s", agentNameToDelete)
				disablePoolAgentChan := make(chan azuredevops.EnableDisablePoolAgentResponse)
				go azdClient.DisablePoolAgentAsync(disablePoolAgentChan, agentPoolID, agentToDelete[0].Agent.ID)
				response := <-disablePoolAgentChan
				if response.Err != nil {
					return response.Err
				}
				logging.Logger.Debugf("Result of call to disenable agent %s: %s", agentNameToDelete, response.Result)

				// avoid scaling down if we had to disable the last agent pod
				logging.Logger.Debugf("Not scaling down yet - last agent pod (%s) was still enabled", agentNameToDelete)
				scaleSizeGauge.Set(0)
				return nil
			}
		}

		// avoid scaling down if last agent pod is active
		if activeAgentNames.Contains(agentNameToDelete) {
			logging.Logger.Debugf("Not scaling down yet - last agent pod (%s) is still active", agentNameToDelete)
			scaleSizeGauge.Set(0)
			return nil
		}
	} else if scalingDirection > 0 {
		// avoid scaling up too soon after a scale down
		now := time.Now()
		nextAllowedScaleUp := lastScaleDown.Add(args.ScaleUp.Delay)
		if now.Before(nextAllowedScaleUp) {
			logging.Logger.Debugf("Not scaling down %s from %d - cannot scale up until %s", deployment.FriendlyName, podCount, nextAllowedScaleUp.String())
			scaleUpLimitedCounter.Inc()
			scaleSizeGauge.Set(0)
			return nil
		}

		// avoid scaling up if there are unschedulable pods
		if unschedulablePodCount > 0 {
			logging.Logger.Infof("Not scaling up - there are %d unschedulable pods.", unschedulablePodCount)
			scaleSizeGauge.Set(0)
			return nil
		}

		// get the agent that will be added if we scale up
		agentNameToAdd := fmt.Sprintf("%s-%d", deployment.Name, podsToScaleTo)
		agentToAdd := getAgentDetails(agents, agentNameToAdd)

		if args.Action == "dry-run" {
			logging.Logger.Infof("Dry Run: would have enabled %s", agentNameToAdd)
		} else {
			// TODO: how do agents get created in ADO?
			// does a new pod coming online create the agent in ADO?
			// allowing scale up anyway, but not enabling it, hopefully when it comes online it registers with ADO and that creates it enabled
			if len(agentToAdd) == 1 && !agentToAdd[0].Enabled {
				// enable agent in ADO
				logging.Logger.Debugf("Enabling agent: %s", agentNameToAdd)

				enablePoolAgentChan := make(chan azuredevops.EnableDisablePoolAgentResponse)
				go azdClient.EnablePoolAgentAsync(enablePoolAgentChan, agentPoolID, agentToAdd[0].Agent.ID)
				response := <-enablePoolAgentChan
				if response.Err != nil {
					return response.Err
				}
				logging.Logger.Debugf("Result of call to enable agent %s: %s", agentNameToAdd, response.Result)
			}
		}
	} else {
		logging.Logger.Debugf("No scaling needed!")
		scaleSizeGauge.Set(0)
		return nil
	}

	if args.Action == "dry-run" {
		logging.Logger.Infof("Dry Run: would have scaled %s from %d to %d pods", deployment.FriendlyName, podCount, podsToScaleTo)
		scaleSizeGauge.Set(0)
		return nil
	}

	// scale the deployment
	logging.Logger.Infof("Scaling %s from %d to %d pods", deployment.FriendlyName, podCount, podsToScaleTo)
	scaleErr := k8sClient.Sync().Scale(deployment, podsToScaleTo)
	if scaleErr != nil {
		return scaleErr
	}

	// apply metrics
	if scalingDirection < 0 {
		lastScaleDown = time.Now()
		scaleDownCounter.Inc()
	} else {
		lastScaleUp = time.Now()
		scaleUpCounter.Inc()
	}
	scaleSizeGauge.Set(float64(podsToScaleTo - podCount))

	return nil
}

func getAgentDetails(agents azuredevops.PoolAgentsResponse, agentName string) []azuredevops.AgentDetails {
	for i, agent := range agents.Agents {
		if agent.Name == agentName {
			return agents.Agents[i:i]
		}
	}
	return make([]azuredevops.AgentDetails, 0)
}

func calculatePodsToScaleTo(activeAgentCount int32, queuedJobCount int32, podCount int32, min int32, max int32) (int32, int32) {
	desiredAgentCount := math.MaxInt32(min, math.MinInt32(max, activeAgentCount+queuedJobCount))
	logging.Logger.Debugf("desiredAgentCount: %d (activeAgentCount: %d + queuedJobCount: %d, min: %d, max: %d)", desiredAgentCount, activeAgentCount, queuedJobCount, min, max)

	scalingDirection := math.MaxInt32(-1, math.MinInt32(1, desiredAgentCount-podCount))
	logging.Logger.Debugf("scalingDirection: %d (desiredAgentCount: %d - podCount: %d)", scalingDirection, desiredAgentCount, podCount)

	podsToScaleTo := podCount + scalingDirection
	logging.Logger.Debugf("podsToScaleTo: %d (podCount: %d, scalingDirection: %d)", podsToScaleTo, podCount, scalingDirection)

	return scalingDirection, podsToScaleTo
}

func analyzePods(pods kubernetes.Pods) (int32, collections.StringSet, int32, int32, int32, int32) {
	podCount := int32(len(pods.Pods))
	podNames := make(collections.StringSet)
	runningPodCount, pendingPodCount, unschedulablePodCount, failedPodCount := int32(0), int32(0), int32(0), int32(0)
	for _, pod := range pods.Pods {
		podNames.Add(pod.Name)
		if pod.Status.Phase == corev1.PodRunning {
			allContainersRunning := true
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.State.Running == nil || containerStatus.State.Terminated != nil {
					allContainersRunning = false
					break
				}
			}
			if allContainersRunning {
				runningPodCount = runningPodCount + 1
			} else {
				pendingPodCount = pendingPodCount + 1
			}
		} else if pod.Status.Phase == corev1.PodPending {
			pendingPodCount = pendingPodCount + 1
			for _, podCondition := range pod.Status.Conditions {
				if podCondition.Type == corev1.PodScheduled && podCondition.Status == corev1.ConditionFalse && podCondition.Reason == corev1.PodReasonUnschedulable {
					unschedulablePodCount = unschedulablePodCount + 1
				}
			}
		} else {
			failedPodCount = failedPodCount + 1
		}
	}
	logging.Logger.Debugf("Found %d pods (%d running, %d pending, %d failed, %d unschedulable)", podCount, runningPodCount, pendingPodCount, failedPodCount, unschedulablePodCount)
	return podCount, podNames, runningPodCount, pendingPodCount, unschedulablePodCount, failedPodCount
}

func getAgentsJobsAndPods(azdClient azuredevops.ClientAsync, agentPoolID int, k8sClient kubernetes.ClientAsync, deployment *kubernetes.Workload) (azuredevops.PoolAgentsResponse, azuredevops.JobRequestsResponse, kubernetes.Pods, error) {
	agentsChan := make(chan azuredevops.PoolAgentsResponse)
	jobsChan := make(chan azuredevops.JobRequestsResponse)
	podsChan := make(chan kubernetes.Pods)

	go azdClient.ListPoolAgentsAsync(agentsChan, agentPoolID)
	go azdClient.ListJobRequestsAsync(jobsChan, agentPoolID)
	go k8sClient.GetPodsAsync(podsChan, deployment)

	agents := <-agentsChan
	if agents.Err != nil {
		return azuredevops.PoolAgentsResponse{}, azuredevops.JobRequestsResponse{}, kubernetes.Pods{}, agents.Err
	}

	jobs := <-jobsChan
	if jobs.Err != nil {
		return azuredevops.PoolAgentsResponse{}, azuredevops.JobRequestsResponse{}, kubernetes.Pods{}, jobs.Err
	}

	pods := <-podsChan
	if pods.Err != nil {
		return azuredevops.PoolAgentsResponse{}, azuredevops.JobRequestsResponse{}, kubernetes.Pods{}, pods.Err
	}

	return agents, jobs, pods, nil
}

func getActiveAgentNames(agents []azuredevops.AgentDetails, podNames collections.StringSet) collections.StringSet {
	activeAgentNames := make(collections.StringSet)
	for _, agent := range agents {
		if strings.EqualFold(agent.Status, "online") {
			if podNames.Contains(agent.Name) && agent.AssignedRequest != nil {
				activeAgentNames.Add(agent.Name)
			}
		}
	}
	return activeAgentNames
}

func getQueuedJobCount(jobs []azuredevops.JobRequest, activeAgentNames collections.StringSet) int32 {
	queuedJobCount := int32(0)
	for _, job := range jobs {
		if job.IsQueuedOrRunning() && job.ReservedAgent == nil {
			if job.MatchesAllAgentsInPool {
				queuedJobCount = queuedJobCount + 1
			} else if len(job.MatchedAgents) > 0 {
				for _, agent := range job.MatchedAgents {
					if activeAgentNames.Contains(agent.Name) {
						queuedJobCount = queuedJobCount + 1
						break
					}
				}
			}
		}
	}
	return queuedJobCount
}
