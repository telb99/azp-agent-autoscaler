# get all pools
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools" >alpha_all_pools.json

# "id": 45,
# "name": "kubernetes-agents-alpha-sandbox",

# id=51
# "name": "kubernetes-agents-alpha-early-adopter-PR",

# get pool by pool name:
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools?poolName=kubernetes-agents-alpha-early-adopter-PR" >alpha_sandbox_pr_pool.json

# get all pool agents with minimal details:
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/agents" >alpha_sandbox_pr_all_agents_minimal.json

# get all pool agents with extra details:
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/agents?includeCapabilities=true&includeAssignedRequest=true&includeLastCompletedRequest=true" >alpha_sandbox_pr_all_agents.json
# "id": 843,
# "name": "azp-agent-early-adopter-pr-31",

# get pool agent by name with extra details
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/agents?includeCapabilities=true&includeAssignedRequest=true&includeLastCompletedRequest=true&agentName=azp-agent-early-adopter-pr-31" >alpha_sandbox_pr_agent_31.json

# get pool agent by name with minimal details
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/agents?agentName=azp-agent-early-adopter-pr-31" >alpha_sandbox_pr_agent_31_minimal.json

# get pool job requests
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/jobrequests" >alpha_sandbox_pr_jobs.json


# patch the enable property on an agent:

# disable agent
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN -X PATCH "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/agents/708" -H 'Content-Type: application/json' -H 'Accept: application/json;api-version=7.1-preview.1' -d '{"id":708,"enabled":false}' >agent_708_disable_result.json

# enable agent
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN -X PATCH "https://dev.azure.com/$env:org/_apis/distributedtask/pools/45/agents/708" -H 'Content-Type: application/json' -H 'Accept: application/json;api-version=7.1-preview.1' -d '{"id":708,"enabled":true}' >agent_708_enable_result.json
