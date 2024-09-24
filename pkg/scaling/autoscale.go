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
	// scaling down takes multiple passes to ensure that the agent is disabled and not active
	// don't allow scaling up or re-enabling of the agent to be scaled down until the scaleset has been scaled down
	podsToScaleDownTo = int32(-1)
	lastScaleDown     = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastScaleUp       = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)

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
	desiredAgentsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_desired_agents_count",
		Help: "The desired number of agents",
	})
	queuedPodsGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "azp_agent_autoscaler_queued_pods_count",
		Help: "The number of queued pods",
	})
)

// Reset the auto scale state - for testing purposes
func Reset() {
	podsToScaleDownTo = int32(-1)
	lastScaleDown = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
	lastScaleUp = time.Date(1970, time.January, 1, 0, 0, 0, 0, time.UTC)
}

// Autoscale the agent deployment
func Autoscale(azdClient azuredevops.ClientAsync, agentPoolID int, k8sClient kubernetes.ClientAsync, deployment *kubernetes.Workload, args args.Args) (string, error) {
	// TODO: handle the case where the agent pool has the number of agents that are desired, but the previous operation was to disable the last agent ready for a scale down, but the scale down never happened.

	logging.Logger.Debugf("----- auto scale start: lastScaleDown: %s, lastScaleUp: %s -----", lastScaleDown.String(), lastScaleUp.String())

	// Get active agents, queued jobs, and pods
	agents, jobs, pods, err := getAgentsJobsAndPods(azdClient, agentPoolID, k8sClient, deployment)
	if err != nil {
		return "trying to get agent jobs and pods information", err
	}

	// Get all pod names and statuses
	podCount, podNames, runningPodCount, pendingPodCount, unschedulablePodCount, failedPodCount := analyzePods(pods)

	// Get number of active agents
	activeAgentNames := getActiveAgentNames(agents.Agents, podNames)
	activeAgentCount := int32(len(activeAgentNames))

	// Determine the number of jobs that are queued
	queuedJobCount := getQueuedJobCount(jobs.Jobs, activeAgentNames)

	logging.Logger.Debugf("Found %d active agents and %d queued jobs.", activeAgentCount, queuedJobCount)

	if runningPodCount != podCount {
		if unschedulablePodCount != pendingPodCount || failedPodCount != 0 {
			scaleSizeGauge.Set(0)
			return fmt.Sprintf("Not scaling - there are %d pending pods and %d failed pods.", pendingPodCount, failedPodCount), nil
		}
	}

	// Calculate number of pods to scale to taking into account min and max limits
	desiredAgentCount, scalingDirection, podsToScaleTo := calculatePodsToScaleTo(activeAgentCount, queuedJobCount, podCount, args.Min, args.Max)

	// Update metrics
	totalAgentsGauge.Set(float64(podCount))
	activeAgentsGauge.Set(float64(activeAgentCount))
	pendingAgentsGauge.Set(float64(pendingPodCount))
	failedAgentsGauge.Set(float64(failedPodCount))
	desiredAgentsGauge.Set(float64(desiredAgentCount))
	queuedPodsGauge.Set(float64(queuedJobCount))

	// avoid scaling up or down if there are unschedulable pods
	if unschedulablePodCount > 0 {
		scaleSizeGauge.Set(0)
		return fmt.Sprintf("Not scaling - there are %d unschedulable pods", unschedulablePodCount), nil
	}

	if scalingDirection == 0 {
		scaleSizeGauge.Set(0)
		return fmt.Sprintf("%d agent pods, scaling not needed (wants %d)", podCount, desiredAgentCount), nil
	} else {
		// get the agent that will be affected by any scaling activity
		affectedPod := podsToScaleTo
		if scalingDirection >= 0 {
			affectedPod = podsToScaleTo - 1
		}
		affectedAgents := getAgentDetails(agents, deployment.Name, affectedPod)
		if len(affectedAgents) == 0 {
			return fmt.Sprintf("trying to get agent details for %s-%d", deployment.Name, affectedPod), fmt.Errorf("unable to locate agent details by HOSTNAME in agent capabilities")
		} else if len(affectedAgents) > 1 {
			return fmt.Sprintf("trying to get agent details for %s-%d", deployment.Name, affectedPod), fmt.Errorf("found %d agents matching by HOSTNAME in agent capabilities", len(affectedAgents))
		}

		affectedAgent := affectedAgents[0]

		if scalingDirection < 0 {
			if podsToScaleDownTo < 0 {
				// avoid scaling down too soon after a scale up
				now := time.Now()
				nextAllowedScaleDown := lastScaleUp.Add(args.ScaleDown.Delay)
				if now.Before(nextAllowedScaleDown) {
					scaleDownLimitedCounter.Inc()
					scaleSizeGauge.Set(0)
					return fmt.Sprintf("Not scaling down %s from %d to %d (wants %d) - cannot scale down until %s", deployment.FriendlyName, podCount, podsToScaleTo, desiredAgentCount, nextAllowedScaleDown.String()), nil
				}
				podsToScaleDownTo = podsToScaleTo
			}

			// if it is enabled, disable it so that it won't pick up more jobs
			if affectedAgent.Enabled {
				if args.Action == "dry-run" {
					logging.Logger.Debugf("Dry Run: would have disabled %s", affectedAgent.Name)
				} else {
					// if the pod that will be deleted when we scale down exists and is enabled, disable it and return...
					// we don't want to scale down if there's a chance the agent pod can pick up a job before the scale down happens
					logging.Logger.Debugf("Disabling agent %s", affectedAgent.Name)

					disablePoolAgentChan := make(chan azuredevops.EnableDisablePoolAgentResponse)
					go azdClient.DisablePoolAgentAsync(disablePoolAgentChan, agentPoolID, affectedAgent.Agent.ID)
					response := <-disablePoolAgentChan
					if response.Err != nil {
						return fmt.Sprintf("trying to disable agent: %s", affectedAgent.Name), response.Err
					}

					// avoid scaling down if we had to disable the last agent pod
					scaleSizeGauge.Set(0)
					return fmt.Sprintf("Not scaling down yet - agent %s was still enabled", affectedAgent.Name), nil
				}
			}

			// avoid scaling down if last agent pod is active
			if activeAgentNames.Contains(affectedAgent.Name) {
				scaleSizeGauge.Set(0)
				return fmt.Sprintf("Not scaling down yet - agent %s is still active", affectedAgent.Name), nil
			}

			// if there is a job that finished recently, avoid scaling down to give the pod chance to finish updating ADO with logs and state
			if affectedAgent.LastCompletedRequest != nil {
				if affectedAgent.AssignedRequest != nil {
					scaleSizeGauge.Set(0)
					logging.Logger.Debugf("AssignedRequest - assigned request FinishTime: %s, Result: %s, RequestID: %d", affectedAgent.AssignedRequest.FinishTime, affectedAgent.AssignedRequest.Result, affectedAgent.AssignedRequest.RequestID)
					return "while trying to scale down", fmt.Errorf("agent %s still has an assigned request", affectedAgent.Name)
				}
				logging.Logger.Debugf("LastCompletedRequest - FinishTime: %s, Result: %s, RequestId: %d", affectedAgent.LastCompletedRequest.FinishTime, affectedAgent.LastCompletedRequest.Result, affectedAgent.LastCompletedRequest.RequestID)
				jobFinishTime, err := time.Parse(time.RFC3339Nano, affectedAgent.LastCompletedRequest.FinishTime)
				if err != nil {
					scaleSizeGauge.Set(0)
					return fmt.Sprintf("Not scaling down yet - agent %s, unable to parse last completed request finish time: %s", affectedAgent.Name, affectedAgent.LastCompletedRequest.FinishTime), nil
				}
				minimumJobSettleTime := time.Now().UTC().Add(time.Duration(-30) * time.Second)
				logging.Logger.Debugf("LastCompletedRequest - checking FinishTime: %s vs minimumJobSettleTime: %s, difference: %d", affectedAgent.LastCompletedRequest.FinishTime, minimumJobSettleTime, jobFinishTime.Compare(minimumJobSettleTime))
				if jobFinishTime.After(minimumJobSettleTime) {
					scaleSizeGauge.Set(0)
					return fmt.Sprintf("Not scaling down yet - agent %s recently finished the last job (at %s)", affectedAgent.Name, affectedAgent.LastCompletedRequest.FinishTime), nil
				}
			}
		} else if scalingDirection > 0 {
			// avoid scaling up too soon after a scale down
			now := time.Now()
			nextAllowedScaleUp := lastScaleDown.Add(args.ScaleUp.Delay)
			if now.Before(nextAllowedScaleUp) {
				scaleUpLimitedCounter.Inc()
				scaleSizeGauge.Set(0)
				return fmt.Sprintf("Not scaling up %s from %d to %d (wants %d) - cannot scale up until %s", deployment.FriendlyName, podCount, podsToScaleTo, desiredAgentCount, nextAllowedScaleUp.String()), nil
			}

			// TODO: how do agents get created in ADO?
			// does a new pod coming online create the agent in ADO?
			// allowing scale up anyway, but not enabling it, hopefully when it comes online it registers with ADO and that creates it enabled
			if !affectedAgent.Enabled {
				if args.Action == "dry-run" {
					logging.Logger.Debugf("Dry Run: would have enabled %s", affectedAgent.Name)
				} else {
					// enable agent in ADO
					logging.Logger.Debugf("Enabling agent %s", affectedAgent.Name)

					enablePoolAgentChan := make(chan azuredevops.EnableDisablePoolAgentResponse)
					go azdClient.EnablePoolAgentAsync(enablePoolAgentChan, agentPoolID, affectedAgent.Agent.ID)
					response := <-enablePoolAgentChan
					if response.Err != nil {
						return fmt.Sprintf("trying to enable agent %s", affectedAgent.Name), response.Err
					}
				}
			}
		}
	}

	if args.Action == "dry-run" {
		scaleSizeGauge.Set(0)
		return fmt.Sprintf("Dry Run: would have scaled %s from %d to %d pods (wants %d)", deployment.FriendlyName, podCount, podsToScaleTo, desiredAgentCount), nil
	}

	// scale the deployment
	logging.Logger.Debugf("Scaling %s from %d to %d pods (wants %d)", deployment.FriendlyName, podCount, podsToScaleTo, desiredAgentCount)
	scaleErr := k8sClient.Sync().Scale(deployment, podsToScaleTo)
	if scaleErr != nil {
		return fmt.Sprintf("trying to scale from %d to %d", podCount, podsToScaleTo), scaleErr
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

	podsToScaleDownTo = -1

	return fmt.Sprintf("Scaled %s from %d to %d (wants %d)", deployment.FriendlyName, podCount, podsToScaleTo, desiredAgentCount), nil
}

func getAgentDetails(agents azuredevops.PoolAgentsResponse, deploymentName string, podNumber int32) []azuredevops.AgentDetails {
	podName := fmt.Sprintf("%s-%d", deploymentName, podNumber)

	for i, agent := range agents.Agents {
		agentHostName := agent.SystemCapabilities["HOSTNAME"]
		if agentHostName == podName {
			enabledMessage := "ENABLED"
			if !agent.Enabled {
				enabledMessage = "disabled"
			}
			activeMessage := "ACTIVE"
			if agent.AssignedRequest == nil {
				activeMessage = "inactive"
			}
			statusMessage := "ONLINE"
			if agent.Status != "online" {
				statusMessage = agent.Status
			}
			logging.Logger.Debugf("found agent: %s (ID %d), pod %s [%s | %s | %s]", agent.Name, agent.ID, podName, activeMessage, enabledMessage, statusMessage)
			return agents.Agents[i : i+1]
		} else {
			logging.Logger.Tracef("getAgentDetails - not match: agent.Name: %s, deploymentName: %s, podNumber: %d, podName: %s, i: %d, agentHostName: %s", agent.Name, deploymentName, podNumber, podName, i, agentHostName)
		}
	}
	logging.Logger.Warnf("unable to find agent details: deploymentName: %s, podNumber:%d, podName: %s", deploymentName, podNumber, podName)
	return make([]azuredevops.AgentDetails, 0)
}

func calculatePodsToScaleTo(activeAgentCount int32, queuedJobCount int32, podCount int32, min int32, max int32) (int32, int32, int32) {
	desiredAgentCount := math.MaxInt32(min, math.MinInt32(max, activeAgentCount+queuedJobCount))
	logging.Logger.Debugf("desiredAgentCount: %d (activeAgentCount: %d + queuedJobCount: %d, min: %d, max: %d)", desiredAgentCount, activeAgentCount, queuedJobCount, min, max)

	if podsToScaleDownTo > 0 {
		// continue scaling down
		logging.Logger.Debugf("continue scaling down to: %d (podCount: %d)", podsToScaleDownTo, podCount)
		return desiredAgentCount, -1, podsToScaleDownTo
	} else {
		scalingDirection := math.MaxInt32(-1, math.MinInt32(1, desiredAgentCount-podCount))
		logging.Logger.Debugf("scalingDirection: %d (desiredAgentCount: %d - podCount: %d)", scalingDirection, desiredAgentCount, podCount)

		podsToScaleTo := podCount + scalingDirection
		logging.Logger.Debugf("podsToScaleTo: %d (podCount: %d, scalingDirection: %d)", podsToScaleTo, podCount, scalingDirection)
		return desiredAgentCount, scalingDirection, podsToScaleTo
	}
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
			podName := agent.SystemCapabilities["HOSTNAME"]
			if podNames.Contains(podName) && agent.AssignedRequest != nil {
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
