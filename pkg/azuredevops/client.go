package azuredevops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Parameter 1 is the Pool Name
const getPoolsEndpoint = "/_apis/distributedtask/pools?poolName=%s"

// Parameter 1 is the Pool ID
const getPoolAgentsEndpoint = "/_apis/distributedtask/pools/%d/agents?includeCapabilities=true&includeAssignedRequest=true"

// Parameter 1 is the Pool ID
const getPoolJobRequestsEndpoint = "/_apis/distributedtask/pools/%d/jobrequests"

// Parameter 1 is the Pool ID
// Parameter 2 is the Agent ID to patch
const patchPoolAgentEndpoint = "/_apis/distributedtask/pools/%d/agents/%d"

const acceptHeader = "application/json;api-version=7.1-preview.1"

var (
	azdDurations = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "azp_agent_autoscaler_azd_call_duration_seconds",
		Help: "Duration of Azure Devops calls",
	}, []string{"operation"})

	azdCounts = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "azp_agent_autoscaler_azd_call_count",
		Help: "Counts of Azure Devops calls",
	}, []string{"operation"})

	azd429Counts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "azp_agent_autoscaler_azd_call_429_count",
		Help: "Counts of Azure Devops calls returning HTTP 429 (Too Many Requests)",
	})
)

// Client is used to call Azure Devops
type Client interface {
	ListPools() ([]PoolDetails, error)
	ListPoolsByName(poolName string) ([]PoolDetails, error)
	ListPoolAgents(poolID int) ([]AgentDetails, error)
	ListJobRequests(poolID int) ([]JobRequest, error)
	EnablePoolAgent(poolID int, agentID int) (string, error)
	DisablePoolAgent(poolID int, agentID int) (string, error)
}

// ClientImpl is the interface implementation that calls Azure Devops
type ClientImpl struct {
	baseURL string

	token string
}

// Execute a GET request
func (c ClientImpl) executeGetRequest(endpoint string, response interface{}) error {
	request, err := http.NewRequest("GET", c.baseURL+endpoint, nil)

	if err != nil {
		return err
	}

	request.Header.Set("Accept", acceptHeader)
	request.Header.Set("User-Agent", "go-azp-agent-autoscaler")

	request.SetBasicAuth("user", c.token)

	httpClient := http.Client{}
	httpResponse, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != 200 {
		httpErr := NewHTTPError(httpResponse)
		if httpErr.RetryAfter != nil {
			azd429Counts.Inc()
		}
		return httpErr
	}

	err = json.NewDecoder(httpResponse.Body).Decode(response)
	if err != nil {
		return fmt.Errorf("Error - could not parse JSON response from %s: %s", c.baseURL+endpoint, err.Error())
	}

	return nil
}

// Execute a PATCH request
func (c ClientImpl) executePatchRequest(endpoint string, response interface{}, jsonBody string) error {
	payload := []byte(jsonBody)
	request, err := http.NewRequest("PATCH", c.baseURL+endpoint, bytes.NewBuffer(payload))

	if err != nil {
		return err
	}

	request.Header.Set("Accept", acceptHeader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "go-azp-agent-autoscaler")

	request.SetBasicAuth("user", c.token)

	httpClient := http.Client{}
	httpResponse, err := httpClient.Do(request)
	if err != nil {
		return err
	}

	defer httpResponse.Body.Close()

	if httpResponse.StatusCode != 200 {
		httpErr := NewHTTPError(httpResponse)
		if httpErr.RetryAfter != nil {
			azd429Counts.Inc()
		}
		return httpErr
	}

	err = json.NewDecoder(httpResponse.Body).Decode(response)
	if err != nil {
		return fmt.Errorf("Error - could not parse JSON response from %s: %s", c.baseURL+endpoint, err.Error())
	}

	return nil
}

// ListPools retrieves a list of agent pools
func (c ClientImpl) ListPools() ([]PoolDetails, error) {
	return c.ListPoolsByName("")
}

// ListPoolsByName retrieves a list of agent pools with the given name
func (c ClientImpl) ListPoolsByName(poolName string) ([]PoolDetails, error) {
	timer := prometheus.NewTimer(azdDurations.With(prometheus.Labels{"operation": "ListPools"}))
	defer timer.ObserveDuration()
	azdCounts.With(prometheus.Labels{"operation": "ListPools"}).Inc()

	response := new(PoolList)
	endpoint := fmt.Sprintf(getPoolsEndpoint, poolName)
	err := c.executeGetRequest(endpoint, response)
	if err != nil {
		return nil, err
	} else {
		return response.Value, nil
	}
}

// ListPoolAgents retrieves all of the agents in a pool
func (c ClientImpl) ListPoolAgents(poolID int) ([]AgentDetails, error) {
	timer := prometheus.NewTimer(azdDurations.With(prometheus.Labels{"operation": "ListPoolAgents"}))
	defer timer.ObserveDuration()
	azdCounts.With(prometheus.Labels{"operation": "ListPoolAgents"}).Inc()

	response := new(Pool)
	endpoint := fmt.Sprintf(getPoolAgentsEndpoint, poolID)
	err := c.executeGetRequest(endpoint, response)
	if err != nil {
		return nil, err
	} else {
		return response.Value, nil
	}
}

// ListJobRequests retrieves the job requests for a pool
func (c ClientImpl) ListJobRequests(poolID int) ([]JobRequest, error) {
	timer := prometheus.NewTimer(azdDurations.With(prometheus.Labels{"operation": "ListJobRequests"}))
	defer timer.ObserveDuration()
	azdCounts.With(prometheus.Labels{"operation": "ListJobRequests"}).Inc()

	response := new(JobRequests)
	endpoint := fmt.Sprintf(getPoolJobRequestsEndpoint, poolID)
	err := c.executeGetRequest(endpoint, response)
	if err != nil {
		return nil, err
	} else {
		return response.Value, nil
	}
}

// EnableDisablePoolAgentResult the result from calling the Enable/Disable Pool Agent API
type EnableDisablePoolAgentResult struct {
	Value string
	Err   error
}

// EnablePoolAgent enables an agent in a pool
func (c ClientImpl) EnablePoolAgent(poolID int, agentID int) (string, error) {
	timer := prometheus.NewTimer(azdDurations.With(prometheus.Labels{"operation": "EnablePoolAgent"}))
	defer timer.ObserveDuration()
	azdCounts.With(prometheus.Labels{"operation": "EnablePoolAgent"}).Inc()

	response := new(EnableDisablePoolAgentResult)
	endpoint := fmt.Sprintf(patchPoolAgentEndpoint, poolID, agentID)
	jsonBody := fmt.Sprintf(`{"id":%d,"enabled":true}`, agentID)
	err := c.executePatchRequest(endpoint, response, jsonBody)
	if err != nil {
		return "", err
	} else {
		return response.Value, nil
	}
}

// DisablePoolAgent disables an agent in a pool
func (c ClientImpl) DisablePoolAgent(poolID int, agentID int) (string, error) {
	timer := prometheus.NewTimer(azdDurations.With(prometheus.Labels{"operation": "DisablePoolAgent"}))
	defer timer.ObserveDuration()
	azdCounts.With(prometheus.Labels{"operation": "DisablePoolAgent"}).Inc()

	response := new(EnableDisablePoolAgentResult)
	endpoint := fmt.Sprintf(patchPoolAgentEndpoint, poolID, agentID)
	jsonBody := fmt.Sprintf(`{"id":%d,"enabled":false}`, agentID)
	err := c.executePatchRequest(endpoint, response, jsonBody)
	if err != nil {
		return "", err
	} else {
		return response.Value, nil
	}
}
