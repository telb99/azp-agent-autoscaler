# get all pools
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools" >alpha_ea_pr_all_pools.json

# get pool by pool name:
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools?poolName=kubernetes-agents-alpha-early-adopter-PR" >alpha_ea_pr_pool.json
# id=51

# get all pool agents with minimal details:
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/agents" >alpha_ea_pr_all_agents_minimal.json

# get all pool agents with extra details:
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/agents?includeCapabilities=true&includeAssignedRequest=true&includeLastCompletedRequest=true" >alpha_ea_pr_all_agents.json
# "id": 843,
# "name": "azp-agent-early-adopter-pr-31",

# get pool agent by name with extra details
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/agents?includeCapabilities=true&includeAssignedRequest=true&includeLastCompletedRequest=true&agentName=azp-agent-early-adopter-pr-31" >alpha_ea_pr_agent_31.json

# get pool agent by name with minimal details
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/agents?agentName=azp-agent-early-adopter-pr-31" >alpha_ea_pr_agent_31_minimal.json

# get pool job requests
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/jobrequests" >alpha_ea_pr_jobs.json


# patch the enable property on an agent:

# disable agent #31 ID 843
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN -X PATCH "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/agents/843" -H 'Content-Type: application/json' -H 'Accept: application/json;api-version=5.0-preview.1' -d '{"id":843,"enabled":false}' >agent_31_disable_result.json

# enable agent #31 ID 843
curl -u user:$Env:NUGET_PERSONAL_ACCESS_TOKEN -X PATCH "https://dev.azure.com/$env:org/_apis/distributedtask/pools/51/agents/843" -H 'Content-Type: application/json' -H 'Accept: application/json;api-version=5.0-preview.1' -d '{"id":843,"enabled":true}' >agent_31_enable_result.json
