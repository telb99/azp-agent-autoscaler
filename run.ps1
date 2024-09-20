go run main.go -action run -log-level info -name "azp-agent-sandbox" -namespace "cluster" -token "$Env:NUGET_PERSONAL_ACCESS_TOKEN" -url "https://dev.azure.com/$env:org"
