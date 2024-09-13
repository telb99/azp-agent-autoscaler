package scaling

import (
	"fmt"
	"strconv"
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
	scalingPhase     = int32(0) // 0: scaling is idle, 1: scaling in progress
	scalingDirection = int32(0) // 0 = none, +1 = up, -1 = down
	podsToScaleTo    = int32(0)
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
	scalingPhase = int32(0)
	scalingDirection = int32(0)
	podsToScaleTo = int32(0)
}

// Autoscale the agent deployment
func Autoscale(azdClient azuredevops.ClientAsync, agentPoolID int, k8sClient kubernetes.ClientAsync, deployment *kubernetes.Workload, args args.Args) error {
	logging.Logger.Debugf("----- auto scale start: scalingPhase: %d, podsToScaleTo: %d, scalingDirection: %d, lastScaleUp: %s -----", scalingPhase, podsToScaleTo, scalingDirection, lastScaleUp.String())

	// Get active agents, queued jobs, and pods
	agents, jobs, pods, err := getAgentsJobsAndPods(azdClient, agentPoolID, k8sClient, deployment)
	if err != nil {
		return err
	}

	// Get all pod names and statuses
	podCount, podNames, runningPodCount, pendingPodCount, unschedulablePodCount, failedPodCount := analyzePods(pods)

	// Get number of active agents
	activeAgentNames := getActiveAgentNames(agents.Agents, podNames)
	activeAgentPodNames := getActiveAgentPodNames(agents.Agents, podNames)
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

	if scalingPhase == 0 {
		// Calculate number of pods to scale to taking into account min and max limits
		calculatePodsToScaleTo(activeAgentCount, queuedJobCount, podCount, args)
		logging.Logger.Debugf("podsToScaleTo: %d, scalingDirection: %d", podsToScaleTo, scalingDirection)
	} else {
		logging.Logger.Debugf("podsToScaleTo: %d, scalingDirection: %d", podsToScaleTo, scalingDirection)
	}

	// Enable/disable Agents in ADO so they can pickup jobs if they should, and not pickup jobs if we want to scale them down
	podDisabledCount := 0
	for _, agent := range agents.Agents {
		if !agent.Enabled {
			agentName := agent.Name
			agentNumber := strings.TrimPrefix(agentName, fmt.Sprintf("%s-", deployment.Name))
			i, err := strconv.Atoi(agentNumber)
			if err == nil && i >= 0 {
				if int32(i) < podsToScaleTo {
					if !agent.Enabled {
						logging.Logger.Debugf("Enabling agent: %s", agentName)

						// TODO: enable agent in ADO
						enablePoolAgentChan := make(chan azuredevops.EnableDisablePoolAgentResponse)
						go azdClient.EnablePoolAgentAsync(enablePoolAgentChan, agentPoolID, agent.Agent.ID)
						agents := <-enablePoolAgentChan
						if agents.Err != nil {
							return agents.Err
						}
					}
				} else {
					if agent.Enabled {
						logging.Logger.Debugf("Disabling agent: %s", agentName)

						// disable agent in ADO
						disablePoolAgentChan := make(chan azuredevops.EnableDisablePoolAgentResponse)
						go azdClient.DisablePoolAgentAsync(disablePoolAgentChan, agentPoolID, agent.Agent.ID)
						agents := <-disablePoolAgentChan
						if agents.Err != nil {
							return agents.Err
						}

						podDisabledCount = podDisabledCount + 1
					}
				}
			}
		}
	}

	if scalingPhase == 0 {
		// avoid scaling down too soon after a scale up
		if scalingDirection < 0 {
			now := time.Now()
			nextAllowedScaleDown := lastScaleUp.Add(args.ScaleDown.Delay)
			if now.Before(nextAllowedScaleDown) {
				logging.Logger.Debugf("Not scaling down %s from %d - cannot scale down until %s", deployment.FriendlyName, podCount, nextAllowedScaleDown.String())
				scaleDownLimitedCounter.Inc()
				scaleSizeGauge.Set(0)
				return nil
			}
		}

		// avoid scaling up too soon after a scale down
		if scalingDirection > 0 {
			now := time.Now()
			nextAllowedScaleUp := lastScaleDown.Add(args.ScaleUp.Delay)
			if now.Before(nextAllowedScaleUp) {
				logging.Logger.Debugf("Not scaling down %s from %d - cannot scale up until %s", deployment.FriendlyName, podCount, nextAllowedScaleUp.String())
				scaleUpLimitedCounter.Inc()
				scaleSizeGauge.Set(0)
				return nil
			}
		}

		// Avoid scaling up if there are unschedulable pods
		// But still allow scaling down, this way node(s) don't have to be allocated and all of the pods launched before a scale down is allowed
		if scalingDirection > 0 && unschedulablePodCount > 0 {
			logging.Logger.Infof("Not scaling up - there are %d unschedulable pods.", unschedulablePodCount)
			scaleSizeGauge.Set(0)
			return nil
		}

		if scalingDirection == 0 {
			// No scaling needed
			logging.Logger.Debugf("No scaling needed!")
			scaleSizeGauge.Set(0)
			return nil
		}

		// we are scaling now
		scalingPhase = 1
	}

	// wait to scale down if we had to disable pods
	if scalingDirection < 0 && podDisabledCount > 0 {
		logging.Logger.Infof("Not scaling down - had to disable pods, need to wait to make sure they haven't picked up a job")
		scaleSizeGauge.Set(0)
		return nil
	}

	// avoid scaling down if last agent pod is active
	if scalingDirection < 0 && activeAgentCount > 0 {
		maxActivePod := int32(0)
		for i := podCount - 1; i > 0; i-- {
			if activeAgentPodNames.Contains(fmt.Sprintf("%s-%d", deployment.Name, i)) {
				maxActivePod = i
				break
			}
		}
		logging.Logger.Debugf("maxActivePod: %d, podCount: %d", maxActivePod, podCount)
		if maxActivePod >= podCount-1 {
			logging.Logger.Debugf("Not scaling down yet - last agent pod is still active")
			scaleSizeGauge.Set(0)
			return nil
		}
	}

	logging.Logger.Infof("Scaling %s from %d to %d pods", deployment.FriendlyName, podCount, podsToScaleTo)
	scaleErr := k8sClient.Sync().Scale(deployment, podsToScaleTo)
	if scaleErr != nil {
		return scaleErr
	}

	// Apply metrics
	if scalingDirection < 0 {
		lastScaleDown = time.Now()
		scaleDownCounter.Inc()
	} else {
		lastScaleUp = time.Now()
		scaleUpCounter.Inc()
	}
	scaleSizeGauge.Set(float64(podsToScaleTo - podCount))

	// Reset scaling phase
	scalingPhase = 0

	return nil
}

func calculatePodsToScaleTo(activeAgentCount int32, queuedJobCount int32, podCount int32, args args.Args) {
	desiredAgentCount := math.MaxInt32(args.Min, math.MinInt32(args.Max, activeAgentCount+queuedJobCount))
	logging.Logger.Debugf("desiredAgentCount: %d (activeAgentCount: %d + queuedJobCount: %d, min: %d, max: %d)", desiredAgentCount, activeAgentCount, queuedJobCount, args.Min, args.Max)

	scalingDirection = math.MaxInt32(-1, math.MinInt32(1, desiredAgentCount-podCount))
	logging.Logger.Debugf("scalingDirection: %d (desiredAgentCount: %d - podCount: %d)", scalingDirection, desiredAgentCount, podCount)

	podsToScaleTo = podCount + scalingDirection
	logging.Logger.Debugf("podsToScaleTo: %d (podCount: %d, scalingDirection: %d)", podsToScaleTo, podCount, scalingDirection)
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
			agentName := agent.Name
			if agentNames.Contains(agentName) && agent.AssignedRequest != nil {
				activeAgentNames.Add(agent.Name)
			}
		}
	}
	return activeAgentNames
}

func getActiveAgentPodNames(agents []azuredevops.AgentDetails, podNames collections.StringSet) collections.StringSet {
	activeAgentPodNames := make(collections.StringSet)
	for _, agent := range agents {
		if strings.EqualFold(agent.Status, "online") {
			agentName := agent.Name
			if podNames.Contains(agentName) && agent.AssignedRequest != nil {
				activeAgentPodNames.Add(agentName)
			}
		}
	}
	return activeAgentPodNames
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
